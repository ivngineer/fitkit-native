import Foundation
import Testing
import UIKit
@testable import Fitkit

@Suite struct ServerDateTests {
    @Test(arguments: [
        "2026-09-17T14:49:30Z",
        "2026-09-17T14:49:30.602Z",
        "2026-09-17T14:49:30.602123456Z",
        "2026-09-17T17:49:30.602+03:00",
    ])
    func parsesGoTimestamps(_ string: String) throws {
        let date = try #require(ServerDate.parse(string))
        let reference = try #require(ServerDate.parse("2026-09-17T14:49:30Z"))
        #expect(abs(date.timeIntervalSince(reference)) < 1)
    }

    @Test func keepsFractionalSeconds() throws {
        let whole = try #require(ServerDate.parse("2026-09-17T14:49:30Z"))
        let fractional = try #require(ServerDate.parse("2026-09-17T14:49:30.25Z"))
        #expect(abs(fractional.timeIntervalSince(whole) - 0.25) < 0.0001)
    }

    @Test func rejectsGarbage() {
        #expect(ServerDate.parse("yesterday") == nil)
    }
}

@Suite struct DecodingTests {
    @Test func decodesPinsPage() throws {
        let json = """
        {"pins":[{"id":"p1","pinterestId":"900","title":"Street style","description":"","link":"",
        "pinterestUrl":"https://www.pinterest.com/pin/900/","imageUrl":"/media/pins/p1.jpg","width":300,"height":450,
        "dominantColor":"#aabbcc","savedAt":"2026-09-17T14:49:30.602Z"}],"nextCursor":"1789"}
        """
        let page = try APIClient.decoder.decode(PinsPage.self, from: Data(json.utf8))
        #expect(page.pins.count == 1)
        #expect(page.nextCursor == "1789")
        #expect(abs(page.pins[0].aspectRatio - 2.0 / 3.0) < 0.0001)
    }

    @Test func decodesAnalysis() throws {
        let json = """
        {"pinId":"p1","status":"done","error":null,"result":{"provider":"serpapi","demo":false,
        "generatedAt":"2026-09-17T14:49:30.602123Z","items":[{"id":"item-1","label":"Denim jacket","category":"outerwear",
        "box":{"x":0.1,"y":0.2,"width":0.5,"height":0.4},"cropUrl":"https://i.ibb.co/x.jpg",
        "listings":[{"title":"Jacket","merchant":"Shop","url":"https://shop.example/j","price":"$89.00","priceValue":89}]},
        {"id":"item-2","label":"Boots","category":"shoes","box":{"x":0,"y":0,"width":1,"height":1},"listings":[],"error":"Visual search failed for this item."}]}}
        """
        let analysis = try APIClient.decoder.decode(PinAnalysis.self, from: Data(json.utf8))
        let result = try #require(analysis.result)
        #expect(analysis.status == .done)
        #expect(result.items.map(\.symbolName) == ["tshirt", "shoe"])
        #expect(result.items[0].listings.first?.priceValue == 89)
        #expect(result.items[1].error != nil)
    }

    @Test func decodesAnalysisWithoutResult() throws {
        let json = #"{"pinId":"p1","status":"none","result":null,"error":null}"#
        let analysis = try APIClient.decoder.decode(PinAnalysis.self, from: Data(json.utf8))
        #expect(analysis.status == .none)
    }

    @Test func importProgress() throws {
        let json = """
        {"id":"j1","status":"running","pinterestUsername":"jane","discovered":10,"imported":3,"reused":1,"failed":1,
        "error":null,"createdAt":"2026-09-17T14:49:30Z","updatedAt":"2026-09-17T14:49:31Z","finishedAt":null}
        """
        let job = try APIClient.decoder.decode(ImportJob.self, from: Data(json.utf8))
        #expect(job.isActive)
        #expect(job.processed == 5)
        #expect(job.progress == 0.5)
    }
}

@Suite struct MasonryLayoutTests {
    @Test func keepsColumnsBalanced() {
        // One very tall item followed by squares: the squares fill the other column first.
        let columns = MasonryLayout.columns(aspectRatios: [0.5, 1, 1, 1], columnCount: 2)
        #expect(columns == [[0, 3], [1, 2]])
    }

    @Test func preservesOrderAndIncludesEveryItem() {
        let ratios = (0..<50).map { 0.4 + Double($0 % 7) * 0.2 }
        let columns = MasonryLayout.columns(aspectRatios: ratios, columnCount: 3)
        #expect(columns.flatMap { $0 }.sorted() == Array(0..<50))
        for column in columns {
            #expect(column == column.sorted())
        }
    }

    @Test func handlesDegenerateInput() {
        #expect(MasonryLayout.columns(aspectRatios: [], columnCount: 2) == [[], []])
        #expect(MasonryLayout.columns(aspectRatios: [0, -1], columnCount: 0) == [[0, 1]])
    }

    @Test(arguments: [(390.0, 2), (700.0, 3), (1000.0, 4), (1366.0, 5)])
    func columnCountAdaptsToWidth(width: Double, expected: Int) {
        #expect(MasonryLayout.columnCount(for: width) == expected)
    }
}

// MARK: - API client

nonisolated final class StubURLProtocol: URLProtocol, @unchecked Sendable {
    typealias Handler = @Sendable (URLRequest) -> (Int, String)
    nonisolated(unsafe) static var handler: Handler?

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let (status, body) = Self.handler?(request) ?? (500, "")
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: nil)!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

/// The cart tests get their own stub class: Swift Testing runs suites in
/// parallel, so sharing one handler makes them clobber each other.
nonisolated final class CartStubURLProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var handler: StubURLProtocol.Handler?

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let (status, body) = Self.handler?(request) ?? (500, "")
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: nil)!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

@Suite(.serialized) struct APIClientTests {
    let client: APIClient

    init() {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [StubURLProtocol.self]
        client = APIClient(baseURL: URL(string: "http://fitkit.test")!, token: "secret", session: URLSession(configuration: configuration))
    }

    @Test func sendsAuthorizationAndBody() async throws {
        StubURLProtocol.handler = { request in
            guard request.value(forHTTPHeaderField: "Authorization") == "Bearer secret",
                  request.httpMethod == "PUT", request.url?.path == "/v1/me/pinterest" else {
                return (400, "")
            }
            return (200, #"{"id":"u1","email":"a@b.co","pinterestUsername":"jane","referralSource":null,"createdAt":"2026-09-17T14:49:30Z"}"#)
        }
        let user = try await client.setPinterestHandle("@jane")
        #expect(user.pinterestUsername == "jane")
        #expect(user.referralSource == nil)
    }

    @Test func mapsServerErrors() async {
        StubURLProtocol.handler = { _ in
            (422, #"{"error":{"code":"invalid_handle","message":"that doesn't look like a Pinterest username"}}"#)
        }
        await #expect(throws: APIError.server(status: 422, code: "invalid_handle", message: "that doesn't look like a Pinterest username")) {
            try await client.setPinterestHandle("x")
        }
    }

    @Test func mapsUnauthorized() async {
        StubURLProtocol.handler = { _ in (401, #"{"error":{"code":"unauthorized","message":"Sign in to continue."}}"#) }
        await #expect(throws: APIError.unauthorized("Sign in to continue.")) {
            try await client.me()
        }
    }

    @Test func latestImportIsNilWhenNoneExist() async throws {
        StubURLProtocol.handler = { _ in (404, #"{"error":{"code":"not_found","message":"No imports yet."}}"#) }
        let job = try await client.latestImport()
        #expect(job == nil)
    }

    @Test func pinsQueryIncludesCursor() async throws {
        StubURLProtocol.handler = { request in
            let query = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
            let ok = query.contains(URLQueryItem(name: "cursor", value: "123"))
            return ok ? (200, #"{"pins":[],"nextCursor":null}"#) : (400, "")
        }
        let page = try await client.pins(cursor: "123")
        #expect(page.pins.isEmpty)
        #expect(page.nextCursor == nil)
    }

    @Test func hidePinSendsPutAndUnhideSendsDelete() async throws {
        StubURLProtocol.handler = { request in
            request.url?.path == "/v1/pins/p1/hidden" && request.httpMethod == "PUT" ? (204, "") : (400, "")
        }
        try await client.hidePin(id: "p1")

        StubURLProtocol.handler = { request in
            request.url?.path == "/v1/pins/p1/hidden" && request.httpMethod == "DELETE" ? (204, "") : (400, "")
        }
        try await client.unhidePin(id: "p1")

        // The wrong verb reaches the wrong route, so the server error surfaces.
        StubURLProtocol.handler = { _ in (404, #"{"error":{"code":"not_found","message":"Pin not found."}}"#) }
        await #expect(throws: APIError.server(status: 404, code: "not_found", message: "Pin not found.")) {
            try await client.hidePin(id: "p1")
        }
    }

    @Test func cartAddsRemovesAndLists() async throws {
        StubURLProtocol.handler = { request in
            request.url?.path == "/v1/cart/p1" && request.httpMethod == "PUT" ? (204, "") : (400, "")
        }
        try await client.addToCart(id: "p1")

        StubURLProtocol.handler = { request in
            request.url?.path == "/v1/cart/p1" && request.httpMethod == "DELETE" ? (204, "") : (400, "")
        }
        try await client.removeFromCart(id: "p1")

        StubURLProtocol.handler = { request in
            request.url?.path == "/v1/cart" ? (200, #"{"pins":[],"nextCursor":null}"#) : (400, "")
        }
        #expect(try await client.cart().pins.isEmpty)
    }

    @Test func resolvesMediaPaths() {
        #expect(client.resolve("/media/pins/a.jpg")?.absoluteString == "http://fitkit.test/media/pins/a.jpg")
        #expect(client.resolve("https://i.ibb.co/x.jpg")?.absoluteString == "https://i.ibb.co/x.jpg")
        #expect(client.resolve(nil) == nil)
        #expect(client.resolve("") == nil)
    }
}

@Suite(.serialized) struct CartModelTests {
    private let pin = Pin(id: "p1", pinterestId: "900", title: "Coat", description: "", link: "",
                          pinterestUrl: "https://www.pinterest.com/pin/900/", imageUrl: "/media/pins/p1.jpg",
                          width: 300, height: 450, dominantColor: "#aabbcc", savedAt: .now)

    private func client() -> APIClient {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [CartStubURLProtocol.self]
        return APIClient(baseURL: URL(string: "http://fitkit.test")!, token: "t", session: URLSession(configuration: config))
    }

    @MainActor @Test func togglesAddAndRemove() async {
        let cart = CartModel()
        let app = AppModel(defaults: UserDefaults(suiteName: "cart-toggle")!)
        CartStubURLProtocol.handler = { _ in (204, "") }

        await cart.toggle(pin, api: client(), app: app)
        #expect(cart.contains(pin))
        #expect(cart.count == 1)

        await cart.toggle(pin, api: client(), app: app)
        #expect(!cart.contains(pin))
        #expect(cart.error == nil)
    }

    @MainActor @Test func putsThePinBackWhenTheServerFails() async {
        let cart = CartModel()
        let app = AppModel(defaults: UserDefaults(suiteName: "cart-rollback")!)
        CartStubURLProtocol.handler = { _ in (500, #"{"error":{"code":"internal","message":"Something went wrong. Try again."}}"#) }

        await cart.toggle(pin, api: client(), app: app)
        #expect(!cart.contains(pin))
        #expect(cart.error != nil)
    }
}

@Suite struct BundledFontTests {
    @Test func newsreaderIsRegistered() {
        #expect(UIFont(name: Newsreader.bold.fontName, size: 20) != nil)
        #expect(UIFont(name: Newsreader.semibold.fontName, size: 20) != nil)
    }
}
