import Foundation
import Observation

@Observable
final class AppModel {
    enum Phase: Equatable {
        case launching
        case unreachable(String)
        case signedOut
        case signedIn
    }

    private(set) var phase: Phase = .launching
    private(set) var user: User?
    var isOnboardingPresented = false

    var baseURL: URL {
        didSet { defaults.set(baseURL.absoluteString, forKey: Self.baseURLKey) }
    }

    private var token: String?
    private let tokenStore: TokenStore
    private let defaults: UserDefaults

    static let baseURLKey = "serverBaseURL"

    init(tokenStore: TokenStore = TokenStore(), defaults: UserDefaults = .standard) {
        self.tokenStore = tokenStore
        self.defaults = defaults
        self.baseURL = Self.initialBaseURL(defaults: defaults)
    }

    var api: APIClient { APIClient(baseURL: baseURL, token: token) }

    static var bundledBaseURL: URL {
        let configured = Bundle.main.object(forInfoDictionaryKey: "FitkitAPIBaseURL") as? String
        return configured.flatMap(URL.init(string:)) ?? URL(string: "http://localhost:8080")!
    }

    private static func initialBaseURL(defaults: UserDefaults) -> URL {
        if let override = defaults.string(forKey: baseURLKey), let url = URL(string: override) {
            return url
        }
        return bundledBaseURL
    }

    // MARK: Session

    func bootstrap() async {
        if ProcessInfo.processInfo.arguments.contains("-resetSession") {
            tokenStore.clear()
        }
        guard let stored = tokenStore.read() else {
            phase = .signedOut
            return
        }
        token = stored
        phase = .launching
        do {
            user = try await api.me()
            phase = .signedIn
        } catch APIError.unauthorized {
            clearSession()
        } catch {
            phase = .unreachable(error.localizedDescription)
        }
    }

    func signIn(email: String, password: String) async throws {
        let response = try await api.signIn(email: email, password: password)
        startSession(response)
    }

    func signUp(email: String, password: String) async throws {
        let response = try await api.signUp(email: email, password: password)
        startSession(response)
        isOnboardingPresented = true
    }

    func signOut() async {
        try? await api.signOut()
        clearSession()
    }

    func deleteAccount() async throws {
        try await api.deleteAccount()
        clearSession()
    }

    func update(_ user: User) {
        self.user = user
    }

    /// Signs out when the server rejects the session; returns true if it did.
    @discardableResult
    func handleUnauthorized(_ error: Error) -> Bool {
        guard case APIError.unauthorized = error else { return false }
        clearSession()
        return true
    }

    private func startSession(_ response: AuthResponse) {
        token = response.token
        tokenStore.save(response.token)
        user = response.user
        phase = .signedIn
    }

    private func clearSession() {
        token = nil
        user = nil
        tokenStore.clear()
        isOnboardingPresented = false
        phase = .signedOut
    }
}
