import Foundation

/// Keeps the signed-in account, its pins, the cart and each pin's breakdown
/// on this device, so Fitkit opens and browses without reaching the server.
///
/// Everything lives in Application Support rather than Caches, so the system
/// doesn't purge it under storage pressure. It can all be downloaded again,
/// so it's left out of backups.
final class LocalStore {
    let root: URL
    let images: ImageCache

    nonisolated static var defaultRoot: URL {
        URL.applicationSupportDirectory.appending(path: "Offline", directoryHint: .isDirectory)
    }

    init(root: URL = LocalStore.defaultRoot, images: ImageCache = .shared) {
        self.root = root
        self.images = images
    }

    // MARK: Account

    func loadUser() -> User? { read("account.json") }

    func saveUser(_ user: User) { write(user, to: "account.json") }

    // MARK: Pins

    /// Every pin in the grid, newest first, as of the last full sync.
    func loadPins() -> [Pin] { read("pins.json") ?? [] }

    func savePins(_ pins: [Pin]) { write(pins, to: "pins.json") }

    func loadCart() -> [Pin] { read("cart.json") ?? [] }

    func saveCart(_ pins: [Pin]) { write(pins, to: "cart.json") }

    /// Drops a pin the user removed from Fitkit, along with its breakdown.
    func removePin(id: String) {
        savePins(loadPins().filter { $0.id != id })
        let cart = loadCart()
        if cart.contains(where: { $0.id == id }) { saveCart(cart.filter { $0.id != id }) }
        try? FileManager.default.removeItem(at: analysisURL(pinID: id))
    }

    // MARK: Breakdowns

    func loadAnalysis(pinID: String) -> PinAnalysis? {
        guard let data = try? Data(contentsOf: analysisURL(pinID: pinID)) else { return nil }
        return try? Self.decoder.decode(PinAnalysis.self, from: data)
    }

    /// Only settled results are worth keeping; a run in progress is stale by
    /// the time anyone reads it back.
    func saveAnalysis(_ analysis: PinAnalysis) {
        guard analysis.isSettled else { return }
        write(analysis, to: analysisURL(pinID: analysis.pinId))
    }

    // MARK: Clearing

    /// Deletes pins, their pieces, shopping links and images from this device.
    /// The account stays, so the user remains signed in.
    func clearPinData() {
        for name in ["pins.json", "cart.json", "analyses"] {
            try? FileManager.default.removeItem(at: root.appending(path: name))
        }
        images.removeAll()
    }

    /// Deletes everything, for sign-out.
    func clearAll() {
        try? FileManager.default.removeItem(at: root)
        images.removeAll()
    }

    /// Bytes on disk, images included.
    nonisolated static func size(of directory: URL) -> Int64 {
        let keys: Set<URLResourceKey> = [.totalFileAllocatedSizeKey, .isRegularFileKey]
        guard let files = FileManager.default.enumerator(at: directory, includingPropertiesForKeys: Array(keys)) else { return 0 }
        var total: Int64 = 0
        for case let file as URL in files {
            guard let values = try? file.resourceValues(forKeys: keys), values.isRegularFile == true else { continue }
            total += Int64(values.totalFileAllocatedSize ?? 0)
        }
        return total
    }

    // MARK: Files

    private func analysisURL(pinID: String) -> URL {
        root.appending(path: "analyses").appending(path: ImageCache.fileName(for: pinID) + ".json")
    }

    private func read<T: Decodable>(_ name: String) -> T? {
        guard let data = try? Data(contentsOf: root.appending(path: name)) else { return nil }
        return try? Self.decoder.decode(T.self, from: data)
    }

    private func write<T: Encodable>(_ value: T, to name: String) {
        write(value, to: root.appending(path: name))
    }

    private func write<T: Encodable>(_ value: T, to url: URL) {
        guard let data = try? Self.encoder.encode(value) else { return }
        prepareRoot()
        try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try? data.write(to: url, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }

    private func prepareRoot() {
        guard !FileManager.default.fileExists(atPath: root.path) else { return }
        try? FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        var values = URLResourceValues()
        values.isExcludedFromBackup = true
        var root = root
        try? root.setResourceValues(values)
    }

    private static let encoder = JSONEncoder()
    private static let decoder = JSONDecoder()
}
