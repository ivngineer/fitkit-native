import Foundation

/// Copies the user's whole library to the device while the server is
/// reachable: every pin, each pin's breakdown and the images the app shows.
/// Runs in the background after the grid loads; anything already on disk is
/// skipped, so later runs only fetch what's new.
final class OfflineSync {
    private var task: Task<Void, Never>?

    static let pageSize = 100
    static let concurrency = 4

    var isRunning: Bool { task != nil }

    func start(api: APIClient, store: LocalStore) {
        task?.cancel()
        task = Task { [weak self] in
            await Self.run(api: api, store: store)
            self?.task = nil
        }
    }

    func cancel() {
        task?.cancel()
        task = nil
    }

    static func run(api: APIClient, store: LocalStore) async {
        guard let pins = await allPins(api: api) else { return }
        store.savePins(pins)

        // A settled breakdown only changes when the user asks for a new
        // search, which saves it again, so only unsettled ones are refetched.
        let unsettled = pins.filter { store.loadAnalysis(pinID: $0.id) == nil }
        await forEach(unsettled) { pin in
            guard let analysis = try? await api.analysis(pinID: pin.id) else { return }
            await store.saveAnalysis(analysis)
        }
        guard !Task.isCancelled else { return }

        let images = pins.flatMap { pin -> [URL] in
            let analysis = store.loadAnalysis(pinID: pin.id)
            let pieces = analysis?.result?.items.map { $0.cropUrl ?? $0.listings.first?.thumbnailUrl } ?? []
            return ([pin.imageUrl] + pieces).compactMap { api.resolve($0) }
        }
        let cache = store.images
        await forEach(images) { url in await cache.prefetch(url) }
    }

    /// Walks every page, or returns nil if any page fails so a partial list
    /// never replaces a complete one.
    private static func allPins(api: APIClient) async -> [Pin]? {
        var pins: [Pin] = []
        var seen = Set<String>()
        var cursor: String?
        repeat {
            guard !Task.isCancelled, let page = try? await api.pins(cursor: cursor, limit: pageSize) else { return nil }
            for pin in page.pins where seen.insert(pin.id).inserted { pins.append(pin) }
            cursor = page.nextCursor
        } while cursor != nil
        return pins
    }

    /// Runs `body` over `items` with a few in flight at once.
    private static func forEach<T: Sendable>(_ items: [T], _ body: @escaping @Sendable (T) async -> Void) async {
        await withTaskGroup(of: Void.self) { group in
            var remaining = items.makeIterator()
            for _ in 0..<concurrency {
                guard let item = remaining.next() else { break }
                group.addTask { await body(item) }
            }
            while await group.next() != nil {
                guard !Task.isCancelled, let item = remaining.next() else { continue }
                group.addTask { await body(item) }
            }
        }
    }
}
