import Foundation

struct User: Codable, Equatable, Sendable {
    let id: String
    let email: String
    var referralSource: String?
    var pinterestUsername: String?
    let createdAt: Date
}

struct AuthResponse: Decodable, Sendable {
    let token: String
    let user: User
}

struct Pin: Codable, Identifiable, Hashable, Sendable {
    let id: String
    let pinterestId: String
    let title: String
    let description: String
    let link: String
    let pinterestUrl: String
    let imageUrl: String
    let width: Int
    let height: Int
    let dominantColor: String
    let savedAt: Date

    /// Width divided by height, with a portrait fallback for unknown sizes.
    var aspectRatio: Double {
        guard width > 0, height > 0 else { return 2.0 / 3.0 }
        return Double(width) / Double(height)
    }
}

struct PinsPage: Decodable, Sendable {
    let pins: [Pin]
    let nextCursor: String?
}

struct ServerError: Codable, Equatable, Sendable {
    let code: String
    let message: String
}

struct ImportJob: Codable, Identifiable, Equatable, Sendable {
    enum Status: String, Codable, Sendable {
        case queued, running, done, failed
    }

    struct JobError: Codable, Equatable, Sendable {
        let code: String
        let message: String
        let retryable: Bool
    }

    let id: String
    let status: Status
    let pinterestUsername: String
    let discovered: Int
    let imported: Int
    let reused: Int
    let failed: Int
    let error: JobError?
    let createdAt: Date
    let updatedAt: Date
    let finishedAt: Date?

    var isActive: Bool { status == .queued || status == .running }
    var processed: Int { imported + reused + failed }

    /// Fraction complete once discovery has found pins; nil while discovering.
    var progress: Double? {
        guard discovered > 0 else { return nil }
        return min(1, Double(processed) / Double(discovered))
    }
}

struct PinAnalysis: Codable, Sendable {
    enum Status: String, Codable, Sendable {
        case none, queued, running, done, failed
    }

    let pinId: String
    let status: Status
    let result: AnalysisResult?
    let error: ServerError?

    /// Finished one way or the other, so it won't change until a new search.
    var isSettled: Bool { status == .done || status == .failed }
}

struct AnalysisResult: Codable, Sendable {
    let items: [DetectedItem]
    let provider: String
    let demo: Bool
    let generatedAt: Date

    /// What the whole look costs at each piece's best match, rounded to
    /// whole units. Prices are added as plain numbers with no currency
    /// conversion, and the symbol comes from the first priced piece.
    /// Nil when no piece has a price.
    var outfitTotal: String? {
        let priced = items.compactMap { $0.listings.first }.filter { $0.amount != nil }
        guard let first = priced.first else { return nil }
        let sum = priced.reduce(0) { $0 + ($1.amount ?? 0) }
        let number = Int(sum.rounded()).formatted()
        return first.currencySymbol + number
    }
}

struct DetectedItem: Codable, Identifiable, Sendable {
    struct Box: Codable, Sendable {
        let x, y, width, height: Double
    }

    let id: String
    let label: String
    let category: String
    let description: String?
    let box: Box
    let cropUrl: String?
    let listings: [Listing]
    let error: String?

    var symbolName: String {
        switch category {
        case "shoes": "shoe"
        case "bag": "handbag"
        case "eyewear": "eyeglasses"
        case "hat": "hat.widebrim"
        case "jewelry": "sparkles"
        case "accessory": "watch.analog"
        case "top", "outerwear", "bottom", "dress": "tshirt"
        default: "tag"
        }
    }
}

struct Listing: Codable, Identifiable, Hashable, Sendable {
    let title: String
    let merchant: String
    let url: String
    let thumbnailUrl: String?
    let price: String?
    let priceValue: Double?
    let currency: String?
    let inStock: Bool?

    var id: String { url }

    /// The numeric price, read from the display string when the store gave
    /// no separate value.
    var amount: Double? {
        if let priceValue { return priceValue }
        guard let price else { return nil }
        let digits = price.drop { !$0.isNumber }.prefix { $0.isNumber || $0 == "." || $0 == "," }
        return Double(digits.replacing(",", with: ""))
    }

    /// Whatever precedes the number in the display price, like "$" or "€".
    var currencySymbol: String {
        guard let price else { return "" }
        return String(price.prefix { !$0.isNumber }).trimmingCharacters(in: .whitespaces)
    }
}

enum ReferralSource: String, CaseIterable, Identifiable, Sendable {
    case instagram, tiktok, pinterest, youtube, friend
    case appStore = "app_store"
    case search, other

    var id: String { rawValue }

    var title: String {
        switch self {
        case .instagram: "Instagram"
        case .tiktok: "TikTok"
        case .pinterest: "Pinterest"
        case .youtube: "YouTube"
        case .friend: "A friend"
        case .appStore: "App Store"
        case .search: "Web search"
        case .other: "Somewhere else"
        }
    }

    var symbolName: String {
        switch self {
        case .instagram: "camera"
        case .tiktok: "music.note"
        case .pinterest: "pin"
        case .youtube: "play.rectangle"
        case .friend: "person.2"
        case .appStore: "app.badge"
        case .search: "magnifyingglass"
        case .other: "ellipsis.circle"
        }
    }
}
