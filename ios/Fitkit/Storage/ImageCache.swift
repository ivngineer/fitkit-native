import CryptoKit
import Foundation
import UIKit

/// Pin photos, item crops and listing thumbnails, kept on disk so they show
/// up offline. Images are immutable per URL, so a file never goes stale.
nonisolated final class ImageCache: @unchecked Sendable {
    static let shared = ImageCache(directory: LocalStore.defaultRoot.appending(path: "images", directoryHint: .isDirectory))

    let directory: URL
    private let memory = NSCache<NSString, UIImage>()
    private let session: URLSession

    init(directory: URL, session: URLSession? = nil) {
        self.directory = directory
        // The disk copy is the cache, so skip URLCache's second one.
        self.session = session ?? {
            let configuration = URLSessionConfiguration.default
            configuration.urlCache = nil
            configuration.timeoutIntervalForRequest = 30
            return URLSession(configuration: configuration)
        }()
        memory.countLimit = 300
    }

    /// Returns the image from memory or disk, downloading and keeping it if
    /// this device hasn't seen it yet.
    func image(for url: URL) async throws -> UIImage {
        let key = url.absoluteString as NSString
        if let image = memory.object(forKey: key) { return image }
        let data = try await data(for: url)
        guard let image = await UIImage(data: data)?.byPreparingForDisplay() else {
            throw URLError(.cannotDecodeContentData)
        }
        memory.setObject(image, forKey: key)
        return image
    }

    /// Downloads the image to disk without decoding it.
    func prefetch(_ url: URL) async {
        guard !contains(url) else { return }
        _ = try? await data(for: url)
    }

    func contains(_ url: URL) -> Bool {
        FileManager.default.fileExists(atPath: fileURL(for: url).path)
    }

    func removeAll() {
        memory.removeAllObjects()
        try? FileManager.default.removeItem(at: directory)
    }

    private func data(for url: URL) async throws -> Data {
        let file = fileURL(for: url)
        if let data = try? Data(contentsOf: file) { return data }
        let (data, response) = try await session.data(from: url)
        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            throw URLError(.badServerResponse)
        }
        guard !data.isEmpty else { throw URLError(.zeroByteResource) }
        try? FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try? data.write(to: file, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
        return data
    }

    func fileURL(for url: URL) -> URL {
        directory.appending(path: Self.fileName(for: url.absoluteString))
    }

    /// A filesystem-safe name: short plain IDs as they are, anything else hashed.
    static func fileName(for key: String) -> String {
        let plain = key.count <= 64 && key.allSatisfy { $0.isASCII && ($0.isLetter || $0.isNumber || $0 == "-" || $0 == "_") }
        if plain { return key }
        return SHA256.hash(data: Data(key.utf8)).map { String(format: "%02x", $0) }.joined()
    }
}
