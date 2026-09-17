import Foundation
import Observation

@Observable
final class PinDetailModel {
    enum State {
        case loading
        case analyzing
        case loaded(AnalysisResult)
        case failed(message: String, canRetry: Bool)
        /// Detection ran and found nothing wearable, so the pin isn't shoppable.
        case noItems(message: String)
    }

    private(set) var state: State = .loading
    let pin: Pin

    static let pollInterval: Duration = .seconds(1.5)
    static let maxPolls = 160

    private var task: Task<Void, Never>?

    init(pin: Pin) {
        self.pin = pin
    }

    /// Replaces any in-flight run so polling never outlives the sheet.
    func start(api: APIClient, app: AppModel, force: Bool = false) {
        task?.cancel()
        task = Task { await run(api: api, app: app, force: force) }
    }

    func cancel() {
        task?.cancel()
        task = nil
    }

    /// Starts (or joins) the server-side analysis and polls until it settles.
    func run(api: APIClient, app: AppModel, force: Bool = false) async {
        state = force ? .analyzing : .loading
        do {
            var analysis = try await api.analysis(pinID: pin.id)
            if force || analysis.status == .none || analysis.status == .failed {
                analysis = try await api.startAnalysis(pinID: pin.id, force: force)
            }
            var polls = 0
            while analysis.status == .running || analysis.status == .queued {
                state = .analyzing
                polls += 1
                guard polls <= Self.maxPolls else {
                    state = .failed(message: "This is taking longer than expected. Try again in a bit.", canRetry: true)
                    return
                }
                try await Task.sleep(for: Self.pollInterval)
                analysis = try await api.analysis(pinID: pin.id)
            }
            apply(analysis)
        } catch is CancellationError {
            return
        } catch {
            if app.handleUnauthorized(error) { return }
            state = .failed(message: error.localizedDescription, canRetry: true)
        }
    }

    private func apply(_ analysis: PinAnalysis) {
        switch (analysis.status, analysis.result) {
        case (.done, let result?):
            state = .loaded(result)
        default:
            let error = analysis.error
            let message = error?.message ?? "We couldn't break down this pin."
            if error?.code == "no_items" {
                state = .noItems(message: message)
                return
            }
            state = .failed(message: message, canRetry: error?.code != "not_configured")
        }
    }
}
