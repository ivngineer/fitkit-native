import Foundation
import Testing
@testable import Fitkit

extension LocalStore {
    /// A store in its own temporary directory, so tests never touch the app's.
    static func temporary() -> LocalStore {
        let root = FileManager.default.temporaryDirectory.appending(path: "fitkit-\(UUID().uuidString)")
        return LocalStore(root: root, images: ImageCache(directory: root.appending(path: "images")))
    }
}

@MainActor @Suite struct LocalStoreTests {
    private let store = LocalStore.temporary()

    private func pin(_ id: String) -> Pin {
        Pin(id: id, pinterestId: "900", title: "Coat \(id)", description: "", link: "",
            pinterestUrl: "https://www.pinterest.com/pin/900/", imageUrl: "/media/pins/\(id).jpg",
            width: 300, height: 450, dominantColor: "#aabbcc", savedAt: Date(timeIntervalSince1970: 1_789_000_000))
    }

    private func analysis(_ pinID: String, status: PinAnalysis.Status) throws -> PinAnalysis {
        let json = """
        {"pinId":"\(pinID)","status":"\(status.rawValue)","error":null,"result":{"provider":"demo","demo":true,
        "generatedAt":"2026-09-17T14:49:30Z","items":[{"id":"item-1","label":"Denim jacket","category":"outerwear",
        "box":{"x":0.1,"y":0.2,"width":0.5,"height":0.4},"cropUrl":"/media/crops/c.jpg",
        "listings":[{"title":"Jacket","merchant":"Shop","url":"https://shop.example/j","price":"$89.00","priceValue":89}]}]}}
        """
        return try APIClient.decoder.decode(PinAnalysis.self, from: Data(json.utf8))
    }

    @Test func roundTripsAccountPinsAndCart() {
        let user = User(id: "u1", email: "a@b.co", referralSource: "friend", pinterestUsername: "jane", createdAt: .now)
        store.saveUser(user)
        store.savePins([pin("p1"), pin("p2")])
        store.saveCart([pin("p2")])

        #expect(store.loadUser() == user)
        #expect(store.loadPins() == [pin("p1"), pin("p2")])
        #expect(store.loadCart().map(\.id) == ["p2"])
    }

    @Test func keepsItemsAndLinksOfSettledBreakdownsOnly() throws {
        store.saveAnalysis(try analysis("p1", status: .done))
        store.saveAnalysis(try analysis("p2", status: .running))

        let saved = try #require(store.loadAnalysis(pinID: "p1"))
        #expect(saved.result?.items.first?.label == "Denim jacket")
        #expect(saved.result?.items.first?.listings.first?.url == "https://shop.example/j")
        #expect(store.loadAnalysis(pinID: "p2") == nil)
    }

    @Test func removingAPinDropsItEverywhere() throws {
        store.savePins([pin("p1"), pin("p2")])
        store.saveCart([pin("p1")])
        store.saveAnalysis(try analysis("p1", status: .done))

        store.removePin(id: "p1")

        #expect(store.loadPins().map(\.id) == ["p2"])
        #expect(store.loadCart().isEmpty)
        #expect(store.loadAnalysis(pinID: "p1") == nil)
    }

    @Test func clearingPinDataKeepsTheAccount() throws {
        let user = User(id: "u1", email: "a@b.co", createdAt: .now)
        store.saveUser(user)
        store.savePins([pin("p1")])
        store.saveCart([pin("p1")])
        store.saveAnalysis(try analysis("p1", status: .done))
        let image = URL(string: "http://fitkit.test/media/pins/p1.jpg")!
        try FileManager.default.createDirectory(at: store.images.directory, withIntermediateDirectories: true)
        try Data([1, 2, 3]).write(to: store.images.fileURL(for: image))
        #expect(store.images.contains(image))

        store.clearPinData()

        #expect(store.loadUser() == user)
        #expect(store.loadPins().isEmpty)
        #expect(store.loadCart().isEmpty)
        #expect(store.loadAnalysis(pinID: "p1") == nil)
        #expect(!store.images.contains(image))
    }

    @Test func clearingEverythingForgetsTheAccount() {
        store.saveUser(User(id: "u1", email: "a@b.co", createdAt: .now))
        store.clearAll()
        #expect(store.loadUser() == nil)
        #expect(LocalStore.size(of: store.root) == 0)
    }

    @Test func fileNamesAreSafe() {
        #expect(ImageCache.fileName(for: "p1_abc-2") == "p1_abc-2")
        let hashed = ImageCache.fileName(for: "http://fitkit.test/media/pins/a.jpg")
        #expect(hashed.count == 64)
        #expect(!hashed.contains("/"))
        #expect(ImageCache.fileName(for: "../../etc") != "../../etc")
    }
}
