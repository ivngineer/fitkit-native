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

struct PinAnalysis: Decodable, Sendable {
    enum Status: String, Decodable, Sendable {
        case none, queued, running, done, failed
    }

    let pinId: String
    let status: Status
    let result: AnalysisResult?
    let error: ServerError?
}

struct AnalysisResult: Codable, Sendable {
    let items: [DetectedItem]
    let provider: String
    let demo: Bool
    let generatedAt: Date
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
