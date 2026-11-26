import Foundation

// Buying through Fitkit: "Buy" sends the picked pieces to the server, Fitkit's
// buyers purchase them, and the app follows the order by its number. The status
// is the only progress the app sees; who works on it stays on the server.

/// An order placed with "Buy".
struct FitkitOrder: Codable, Identifiable, Hashable, Sendable {
    struct Item: Codable, Hashable, Sendable {
        let name: String
        let store: String
        let size: String
        let quantity: Int
        let photoUrl: String
        /// Kopiyky, when the store's price is in hryvnias.
        let price: Int?
        /// The store's own price text ("$39.90") when it isn't in hryvnias.
        let listingPrice: String
        let cancelled: Bool

        var displayPrice: String? {
            if let price { return Hryvnia.format(price) }
            return listingPrice.isEmpty ? nil : listingPrice
        }
    }

    let id: String
    let number: String
    /// What the client sees, e.g. "Замовлення підтверджено". Shown as is.
    let status: String
    let photoUrl: String
    /// Kopiyky, when every piece has a price in hryvnias.
    let total: Int?
    let items: [Item]
    let createdAt: Date
}

/// Body of `POST /v1/orders`. Names, stores, links and prices come from the
/// pin's analysis on the server; the app only says which pieces and sizes.
struct OrderRequest: Encodable, Sendable {
    struct Item: Encodable, Sendable {
        let itemId: String
        let listingUrl: String
        let size: String?
        let quantity: Int
        let comment: String?
    }

    let pinId: String
    /// Name or nickname the buyers see.
    let name: String
    /// Same key for a retried tap, so it returns the same order.
    let idempotencyKey: String
    let items: [Item]
}

enum Hryvnia {
    /// "1 799 ₴" from kopiyky.
    static func format(_ kopiyky: Int) -> String {
        (Decimal(kopiyky) / 100).formatted(
            .currency(code: "UAH").locale(Locale(identifier: "uk_UA")).precision(.fractionLength(0...2)))
    }
}
