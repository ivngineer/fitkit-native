import Foundation

enum APIError: LocalizedError, Equatable {
    case unauthorized(String)
    case server(status: Int, code: String, message: String)
    case network(String)
    case invalidResponse

    var errorDescription: String? {
        switch self {
        case .unauthorized(let message): message
        case .server(_, _, let message): message
        case .network(let message): message
        case .invalidResponse: "The server sent a response Fitkit didn't understand."
        }
    }

    /// The server couldn't be reached at all, as opposed to answering with an error.
    var isNetwork: Bool {
        if case .network = self { return true }
        return false
    }

    var code: String? {
        if case .server(_, let code, _) = self { return code }
        return nil
    }
}

/// Talks to the Fitkit server. All scraping, provider keys and accounts live
/// server-side; the app only ever sees Fitkit's own API.
struct APIClient: Sendable {
    var baseURL: URL
    var token: String?
    var session: URLSession = .shared
    /// Told after every request whether the server answered at all, so the
    /// app can switch between online and offline browsing.
    var onReachability: (@MainActor @Sendable (Bool) -> Void)?

    // MARK: Auth

    func signUp(email: String, password: String) async throws -> AuthResponse {
        try await send("POST", "/v1/auth/signup", body: ["email": email, "password": password])
    }

    func signIn(email: String, password: String) async throws -> AuthResponse {
        try await send("POST", "/v1/auth/login", body: ["email": email, "password": password])
    }

    func signOut() async throws {
        try await sendEmpty("POST", "/v1/auth/logout")
    }

    // MARK: Account

    func me() async throws -> User {
        try await send("GET", "/v1/me")
    }

    func deleteAccount() async throws {
        try await sendEmpty("DELETE", "/v1/me")
    }

    func setReferralSource(_ source: ReferralSource) async throws -> User {
        try await send("PUT", "/v1/me/referral-source", body: ["source": source.rawValue])
    }

    func setPinterestHandle(_ handle: String) async throws -> User {
        try await send("PUT", "/v1/me/pinterest", body: ["handle": handle])
    }

    // MARK: Imports

    func startImport() async throws -> ImportJob {
        try await send("POST", "/v1/imports")
    }

    func latestImport() async throws -> ImportJob? {
        do {
            return try await send("GET", "/v1/imports/latest")
        } catch APIError.server(404, _, _) {
            return nil
        }
    }

    func importJob(id: String) async throws -> ImportJob {
        try await send("GET", "/v1/imports/\(id)")
    }

    // MARK: Pins

    func pins(cursor: String? = nil, limit: Int = 60) async throws -> PinsPage {
        var query = [URLQueryItem(name: "limit", value: String(limit))]
        if let cursor { query.append(URLQueryItem(name: "cursor", value: cursor)) }
        return try await send("GET", "/v1/pins", query: query)
    }

    /// Hides a pin from the grid. The pin stays saved on the server, so a
    /// re-import never brings it back and it can be restored later.
    func hidePin(id: String) async throws {
        try await sendEmpty("PUT", "/v1/pins/\(id)/hidden")
    }

    func unhidePin(id: String) async throws {
        try await sendEmpty("DELETE", "/v1/pins/\(id)/hidden")
    }

    // MARK: Cart

    /// Pins the user set aside to buy, most recently added first.
    func cart() async throws -> PinsPage {
        try await send("GET", "/v1/cart")
    }

    func addToCart(id: String) async throws {
        try await sendEmpty("PUT", "/v1/cart/\(id)")
    }

    func removeFromCart(id: String) async throws {
        try await sendEmpty("DELETE", "/v1/cart/\(id)")
    }

    func analysis(pinID: String) async throws -> PinAnalysis {
        try await send("GET", "/v1/pins/\(pinID)/analysis")
    }

    func startAnalysis(pinID: String, force: Bool = false) async throws -> PinAnalysis {
        let query = force ? [URLQueryItem(name: "force", value: "true")] : []
        return try await send("POST", "/v1/pins/\(pinID)/analysis", query: query)
    }

    // MARK: Checkout

    func addresses() async throws -> [Address] {
        let page: AddressList = try await send("GET", "/v1/addresses")
        return page.addresses
    }

    func createAddress(_ address: Address) async throws -> Address {
        try await send("POST", "/v1/addresses", json: address)
    }

    func updateAddress(_ address: Address) async throws -> Address {
        try await send("PUT", "/v1/addresses/\(address.id)", json: address)
    }

    func deleteAddress(id: String) async throws {
        try await sendEmpty("DELETE", "/v1/addresses/\(id)")
    }

    /// Remembered sizes, keyed by `SizeCategory` raw value.
    func sizes() async throws -> [String: String] {
        let profile: SizeProfile = try await send("GET", "/v1/sizes")
        return profile.sizes
    }

    func setSizes(_ sizes: [String: String]) async throws -> [String: String] {
        let profile: SizeProfile = try await send("PUT", "/v1/sizes", json: SizeProfile(sizes: sizes))
        return profile.sizes
    }

    /// How each listing checks out and, for Shopify stores, its variants.
    func listingOptions(pinID: String, urls: [String]) async throws -> [ListingOptions] {
        struct Body: Encodable { let pinId: String; let urls: [String] }
        struct Response: Decodable { let options: [ListingOptions] }
        let response: Response = try await send("POST", "/v1/listings/options", json: Body(pinId: pinID, urls: urls))
        return response.options
    }

    func createLook(_ request: LookRequest) async throws -> Look {
        try await send("POST", "/v1/looks", json: request)
    }

    func looks() async throws -> [Look] {
        struct Response: Decodable { let looks: [Look] }
        let response: Response = try await send("GET", "/v1/looks")
        return response.looks
    }

    func look(id: String) async throws -> Look {
        try await send("GET", "/v1/looks/\(id)")
    }

    func updateLookStore(lookID: String, merchant: String, _ update: LookStoreUpdate) async throws -> LookStore {
        try await send("PUT", "/v1/looks/\(lookID)/stores/\(merchant)", json: update)
    }

    /// Resolves server-relative media paths ("/media/…") and absolute URLs.
    func resolve(_ path: String?) -> URL? {
        guard let path, !path.isEmpty else { return nil }
        return URL(string: path, relativeTo: baseURL)?.absoluteURL
    }

    // MARK: Transport

    private func request(_ method: String, _ path: String, query: [URLQueryItem], body: (any Encodable)?) throws -> URLRequest {
        guard var components = URLComponents(url: baseURL.appending(path: path), resolvingAgainstBaseURL: false) else {
            throw APIError.invalidResponse
        }
        if !query.isEmpty { components.queryItems = query }
        guard let url = components.url else { throw APIError.invalidResponse }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.timeoutInterval = 30
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if let token { request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        if let body {
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try Self.encoder.encode(body)
        }
        return request
    }

    private func perform(_ request: URLRequest) async throws -> Data {
        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: request)
        } catch let error as URLError where error.code == .cancelled {
            throw CancellationError()
        } catch let error as URLError {
            onReachability?(false)
            throw APIError.network(Self.describe(error))
        }
        onReachability?(true)
        guard let http = response as? HTTPURLResponse else { throw APIError.invalidResponse }
        guard (200..<300).contains(http.statusCode) else {
            let serverError = try? Self.decoder.decode([String: ServerError].self, from: data)["error"]
            let message = serverError?.message ?? HTTPURLResponse.localizedString(forStatusCode: http.statusCode).capitalized
            if http.statusCode == 401 { throw APIError.unauthorized(message) }
            throw APIError.server(status: http.statusCode, code: serverError?.code ?? "http_\(http.statusCode)", message: message)
        }
        return data
    }

    private func send<T: Decodable>(_ method: String, _ path: String, query: [URLQueryItem] = [], body: [String: String]? = nil) async throws -> T {
        try await send(method, path, query: query, json: body)
    }

    private func send<T: Decodable>(_ method: String, _ path: String, query: [URLQueryItem] = [], json body: (any Encodable)?) async throws -> T {
        let data = try await perform(try request(method, path, query: query, body: body))
        do {
            return try Self.decoder.decode(T.self, from: data)
        } catch {
            throw APIError.invalidResponse
        }
    }

    private func sendEmpty(_ method: String, _ path: String) async throws {
        _ = try await perform(try request(method, path, query: [], body: nil))
    }

    private static func describe(_ error: URLError) -> String {
        switch error.code {
        case .notConnectedToInternet, .networkConnectionLost:
            "You're offline. Check your connection and try again."
        case .cannotConnectToHost, .cannotFindHost, .timedOut:
            "Couldn't reach the Fitkit server. Try again in a moment."
        default:
            error.localizedDescription
        }
    }

    static let encoder: JSONEncoder = {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        return encoder
    }()

    static let decoder: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { decoder in
            let string = try decoder.singleValueContainer().decode(String.self)
            guard let date = ServerDate.parse(string) else {
                throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "Invalid date \(string)"))
            }
            return date
        }
        return decoder
    }()
}

private struct AddressList: Decodable {
    let addresses: [Address]
}

private struct SizeProfile: Codable {
    let sizes: [String: String]
}

/// Parses RFC 3339 timestamps with any number of fractional-second digits,
/// as produced by Go's time.Time JSON encoding.
nonisolated enum ServerDate {
    static func parse(_ string: String) -> Date? {
        var base = string
        var fraction = 0.0
        if let dot = string.firstIndex(of: ".") {
            let afterDot = string.index(after: dot)
            let zoneStart = string[afterDot...].firstIndex { !$0.isNumber } ?? string.endIndex
            fraction = Double("0." + string[afterDot..<zoneStart]) ?? 0
            base = String(string[..<dot] + string[zoneStart...])
        }
        guard let date = try? Date(base, strategy: .iso8601) else { return nil }
        return date.addingTimeInterval(fraction)
    }
}
