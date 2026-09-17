import Foundation
import Observation

@Observable
final class HomeModel {
    private(set) var pins: [Pin] = []
    private(set) var hasLoaded = false
    private(set) var isLoadingMore = false
    private(set) var loadError: String?
    private(set) var importJob: ImportJob?
    private(set) var importError: String?
    /// Hides a finished job's banner once the user dismisses it.
    var dismissedJobID: String?

    private var nextCursor: String?
    private var pollTask: Task<Void, Never>?

    static let pollInterval: Duration = .seconds(1.5)

    var visibleImportJob: ImportJob? {
        guard let importJob, importJob.id != dismissedJobID else { return nil }
        return importJob
    }

    /// Loads the first page and resumes watching any import in progress.
    func load(api: APIClient, app: AppModel) async {
        await refreshPins(api: api, app: app)
        do {
            let job = try await api.latestImport()
            importJob = job
            if let job, job.isActive {
                watch(job, api: api, app: app)
            } else if job == nil, pins.isEmpty, app.user?.pinterestUsername != nil {
                await startImport(api: api, app: app)
            } else if job?.status == .done {
                dismissedJobID = job?.id
            }
        } catch {
            app.handleUnauthorized(error)
        }
    }

    func refreshPins(api: APIClient, app: AppModel) async {
        do {
            let page = try await api.pins()
            pins = page.pins
            nextCursor = page.nextCursor
            loadError = nil
        } catch is CancellationError {
            return
        } catch {
            if !app.handleUnauthorized(error) { loadError = error.localizedDescription }
        }
        hasLoaded = true
    }

    func loadMoreIfNeeded(after pin: Pin, api: APIClient, app: AppModel) async {
        guard let cursor = nextCursor, !isLoadingMore,
              let index = pins.firstIndex(of: pin), index >= pins.count - 8 else { return }
        isLoadingMore = true
        defer { isLoadingMore = false }
        do {
            let page = try await api.pins(cursor: cursor)
            let known = Set(pins.map(\.id))
            pins.append(contentsOf: page.pins.filter { !known.contains($0.id) })
            nextCursor = page.nextCursor
        } catch {
            app.handleUnauthorized(error)
        }
    }

    func startImport(api: APIClient, app: AppModel) async {
        importError = nil
        do {
            let job = try await api.startImport()
            importJob = job
            dismissedJobID = nil
            watch(job, api: api, app: app)
        } catch {
            if !app.handleUnauthorized(error) { importError = error.localizedDescription }
        }
    }

    /// Removes a pin from the grid right away and tells the server to keep it
    /// hidden; the pin comes back if the request fails.
    func remove(_ pin: Pin, api: APIClient, app: AppModel) async {
        guard let index = pins.firstIndex(of: pin) else { return }
        pins.remove(at: index)
        do {
            try await api.hidePin(id: pin.id)
        } catch {
            pins.insert(pin, at: min(index, pins.count))
            if !app.handleUnauthorized(error) { loadError = error.localizedDescription }
        }
    }

    func stopWatching() {
        pollTask?.cancel()
        pollTask = nil
    }

    private func watch(_ job: ImportJob, api: APIClient, app: AppModel) {
        pollTask?.cancel()
        pollTask = Task { [weak self] in
            var lastSaved = job.imported + job.reused
            var current = job
            while current.isActive, !Task.isCancelled {
                try? await Task.sleep(for: Self.pollInterval)
                guard !Task.isCancelled, let self else { return }
                do {
                    current = try await api.importJob(id: current.id)
                    self.importJob = current
                } catch is CancellationError {
                    return
                } catch {
                    if app.handleUnauthorized(error) { return }
                    continue
                }
                // Show newly imported pins as they land instead of waiting for the end.
                let saved = current.imported + current.reused
                if saved != lastSaved || !current.isActive {
                    lastSaved = saved
                    await self.refreshPins(api: api, app: app)
                }
            }
        }
    }
}
