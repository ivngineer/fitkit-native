import Foundation
import Observation

/// The pins a user set aside to buy. Kept app-wide so the toolbar badge, the
/// grid's quick actions and the cart sheet all agree.
@Observable
final class CartModel {
    private(set) var pins: [Pin] = []
    private(set) var hasLoaded = false
    private(set) var error: String?

    var count: Int { pins.count }

    func contains(_ pin: Pin) -> Bool {
        pins.contains { $0.id == pin.id }
    }

    func load(api: APIClient, app: AppModel) async {
        do {
            pins = try await api.cart().pins
            error = nil
        } catch is CancellationError {
            return
        } catch {
            if !app.handleUnauthorized(error) { self.error = error.localizedDescription }
        }
        hasLoaded = true
    }

    /// Adds or removes in one tap, updating the grid before the round trip and
    /// putting the pin back where it was if the server refuses.
    func toggle(_ pin: Pin, api: APIClient, app: AppModel) async {
        if let index = pins.firstIndex(where: { $0.id == pin.id }) {
            pins.remove(at: index)
            await call(app: app) { try await api.removeFromCart(id: pin.id) } undo: { [weak self] in
                guard let self else { return }
                pins.insert(pin, at: min(index, pins.count))
            }
        } else {
            pins.insert(pin, at: 0)
            await call(app: app) { try await api.addToCart(id: pin.id) } undo: { [weak self] in
                self?.pins.removeAll { $0.id == pin.id }
            }
        }
    }

    /// Hiding a pin from the grid drops it from the cart too: it's gone from
    /// the app, so leaving it queued for checkout would read as a bug.
    func removeFromFitkit(_ pin: Pin, api: APIClient, app: AppModel) async {
        await call(app: app) { try await api.hidePin(id: pin.id) } undo: {}
        if contains(pin) { await toggle(pin, api: api, app: app) }
    }

    func clear() {
        pins = []
        hasLoaded = false
        error = nil
    }

    private func call(app: AppModel, _ work: () async throws -> Void, undo: () -> Void) async {
        do {
            try await work()
            error = nil
        } catch {
            undo()
            if !app.handleUnauthorized(error) { self.error = error.localizedDescription }
        }
    }
}
