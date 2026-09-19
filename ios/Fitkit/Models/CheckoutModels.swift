import Foundation

// Checkout never goes through Fitkit: the server builds links to each store's
// own checkout, prefilled where the store allows, and the app records what
// the user says they ordered.

struct Address: Codable, Identifiable, Hashable, Sendable {
    enum Kind: String, Codable, CaseIterable, Identifiable, Sendable {
        case home
        case npBranch = "np_branch"
        case forwarder

        var id: String { rawValue }

        var title: String {
            switch self {
            case .home: "Home"
            case .npBranch: "Nova Poshta Branch"
            case .forwarder: "Forwarding Address"
            }
        }
    }

    struct Fields: Codable, Hashable, Sendable {
        var firstName = ""
        var lastName = ""
        var line1 = ""
        var line2 = ""
        var city = ""
        var region = ""
        var zip = ""
        var phone = ""
        /// The personal suite or customer ID a forwarder assigns.
        var suiteId = ""
        /// Nova Poshta branch number.
        var npBranch = ""
    }

    var id: String = ""
    var kind: Kind = .home
    var label: String = ""
    /// ISO country code of the delivery point.
    var country: String = ""
    /// "np_shopping", "meest" or "ukraine_express" for forwarding addresses.
    var forwarder: String = ""
    /// Where the parcel finally goes, e.g. UA for a forwarder in Poland.
    var finalCountry: String = ""
    var fields = Fields()
    var isDefault = false
    var createdAt: Date?

    /// A single line for lists and pickers.
    var summary: String {
        let name = [fields.firstName, fields.lastName].joined(separator: " ").trimmingCharacters(in: .whitespaces)
        let place: String = switch kind {
        case .npBranch: "Nova Poshta #\(fields.npBranch), \(fields.city)"
        case .forwarder: "\(Forwarder(rawValue: forwarder)?.title ?? "Forwarder") \(fields.suiteId), \(country)"
        case .home: [fields.line1, fields.city, country].filter { !$0.isEmpty }.joined(separator: ", ")
        }
        return [name, place].filter { !$0.isEmpty }.joined(separator: " · ")
    }

    var title: String { label.isEmpty ? kind.title : label }
}

enum Forwarder: String, CaseIterable, Identifiable, Sendable {
    case npShopping = "np_shopping"
    case meest
    case ukraineExpress = "ukraine_express"

    var id: String { rawValue }

    var title: String {
        switch self {
        case .npShopping: "NP Shopping"
        case .meest: "Meest"
        case .ukraineExpress: "Ukraine Express"
        }
    }
}

/// Categories a size can be remembered for; matches `DetectedItem.category`.
enum SizeCategory: String, CaseIterable, Identifiable, Sendable {
    case top, bottom, dress, outerwear, shoes

    var id: String { rawValue }

    var title: String {
        switch self {
        case .top: "Tops"
        case .bottom: "Bottoms"
        case .dress: "Dresses"
        case .outerwear: "Outerwear"
        case .shoes: "Shoes"
        }
    }
}

enum CheckoutMethod: String, Codable, Sendable {
    case shopifyCart = "shopify_cart"
    case amazonCart = "amazon_cart"
    case affiliateLink = "affiliate_link"
    case productPage = "product_page"

    init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = CheckoutMethod(rawValue: raw) ?? .productPage
    }

    /// Whether the store's link lands in a filled cart rather than on a page.
    var isCart: Bool { self == .shopifyCart || self == .amazonCart }
}

struct ListingOptions: Decodable, Sendable {
    struct Product: Decodable, Sendable {
        struct Option: Decodable, Sendable {
            let name: String
            let values: [String]
        }

        struct Variant: Decodable, Identifiable, Hashable, Sendable {
            let id: Int64
            let title: String
            let options: [String]?
            let available: Bool
            /// Minor units (cents).
            let price: Int
        }

        let title: String
        let options: [Option]?
        let variants: [Variant]
    }

    let url: String
    let merchant: String
    let method: CheckoutMethod
    let product: Product?
}

struct LookRequest: Encodable, Sendable {
    struct Item: Encodable, Sendable {
        let itemId: String
        let listingUrl: String
        var variantId: Int64?
        var size: String?
        var quantity: Int
    }

    let pinId: String
    var addressId: String?
    var items: [Item]
}

struct Money: Codable, Hashable, Sendable {
    let amount: Double
    let currency: String

    var formatted: String {
        guard !currency.isEmpty else { return amount.formatted(.number.precision(.fractionLength(2))) }
        return amount.formatted(.currency(code: currency))
    }
}

struct CheckoutPlan: Codable, Sendable {
    struct Item: Codable, Identifiable, Sendable {
        let itemId: String
        let label: String
        let category: String
        let title: String
        let imageUrl: String?
        let listingUrl: String
        /// The item's own product page, for items not in the store's cart.
        let openUrl: String
        let method: CheckoutMethod
        let inCart: Bool
        let variantId: Int64?
        let variantTitle: String?
        let size: String?
        let sizeAtCheckout: Bool
        let quantity: Int
        let price: Money?

        var id: String { itemId }
    }

    struct PlanAddress: Codable, Sendable {
        let id: String
        let label: String
        let kind: String
        /// "direct" or "forwarder".
        let via: String
        let summary: String
    }

    struct Store: Codable, Identifiable, Sendable {
        let merchant: String
        let name: String
        let method: CheckoutMethod
        let checkoutUrl: String
        let prefilled: Bool
        let items: [Item]
        let subtotals: [Money]
        let address: PlanAddress?
        let note: String?
        let warnings: [String]

        var id: String { merchant }

        /// Items that open on their own page instead of the store's button.
        var separateItems: [Item] {
            guard method.isCart || items.count > 1 else { return [] }
            return items.filter { !$0.inCart && $0.openUrl != checkoutUrl }
        }
    }

    let stores: [Store]
    let totals: [Money]
    let warnings: [String]
    let disclosure: String
}

struct LookStore: Codable, Identifiable, Hashable, Sendable {
    enum Status: String, Codable, CaseIterable, Sendable {
        case pending, ordered, shipped, delivered, skipped

        var title: String {
            switch self {
            case .pending: "Not ordered"
            case .ordered: "Ordered"
            case .shipped: "Shipped"
            case .delivered: "Delivered"
            case .skipped: "Skipped"
            }
        }
    }

    let merchant: String
    var status: Status
    var orderRef: String
    var trackingNo: String
    var carrier: String
    var trackingUrl: String?
    var updatedAt: Date

    var id: String { merchant }
}

struct LookStoreUpdate: Encodable, Sendable {
    var status: LookStore.Status
    var orderRef: String?
    var trackingNo: String?
    var carrier: String?
}

/// Carriers the server knows tracking pages for.
enum Carrier: String, CaseIterable, Identifiable, Sendable {
    case usps, ups, fedex, dhl
    case novaPoshta = "nova_poshta"
    case meest, ukrposhta, inpost
    case royalMail = "royal_mail"

    var id: String { rawValue }

    var title: String {
        switch self {
        case .usps: "USPS"
        case .ups: "UPS"
        case .fedex: "FedEx"
        case .dhl: "DHL"
        case .novaPoshta: "Nova Poshta"
        case .meest: "Meest"
        case .ukrposhta: "Ukrposhta"
        case .inpost: "InPost"
        case .royalMail: "Royal Mail"
        }
    }
}

/// One "Get this look" checkout and what became of each store's order.
struct Look: Codable, Identifiable, Sendable {
    let id: String
    let pinId: String
    let pinTitle: String
    let pinImageUrl: String
    let plan: CheckoutPlan
    var stores: [LookStore]
    let createdAt: Date

    func store(_ merchant: String) -> LookStore? {
        stores.first { $0.merchant == merchant }
    }
}
