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
    /// The server can't be reached, so screens show what's saved on this
    /// device and anything that needs the server is turned off.
    private(set) var isOffline = false
    /// Bumped when the server comes back after being offline, so screens reload.
    private(set) var reconnectCount = 0
    /// Bumped when local data is cleared, so screens drop what they hold.
    private(set) var localDataResetCount = 0

    let store: LocalStore
    let sync = OfflineSync()

    var baseURL: URL {
        didSet { defaults.set(baseURL.absoluteString, forKey: Self.baseURLKey) }
    }

    private var token: String?
    private let tokenStore: TokenStore
    private let defaults: UserDefaults
    private var retryTask: Task<Void, Never>?

    static let baseURLKey = "serverBaseURL"
    static let firstRetryDelay: Duration = .seconds(5)
    static let maxRetryDelay: Duration = .seconds(60)

    init(tokenStore: TokenStore = TokenStore(), defaults: UserDefaults = .standard, store: LocalStore = LocalStore()) {
        self.tokenStore = tokenStore
        self.defaults = defaults
        self.store = store
        self.baseURL = Self.initialBaseURL(defaults: defaults)
    }

    var api: APIClient {
        APIClient(baseURL: baseURL, token: token) { [weak self] reachable in
            self?.setReachable(reachable)
        }
    }

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
            store.clearAll()
        }
        guard let stored = tokenStore.read() else {
            phase = .signedOut
            return
        }
        token = stored
        // Someone who signed in here before goes straight to their saved
        // pins; the server check below catches up in the background.
        if let cached = store.loadUser() {
            user = cached
            phase = .signedIn
            await refreshUser()
            return
        }
        phase = .launching
        do {
            let user = try await api.me()
            self.user = user
            store.saveUser(user)
            phase = .signedIn
        } catch APIError.unauthorized {
            clearSession()
        } catch {
            phase = .unreachable(error.localizedDescription)
        }
    }

    /// Checks the session and picks up account changes made elsewhere.
    func refreshUser() async {
        guard token != nil else { return }
        do {
            let user = try await api.me()
            update(user)
        } catch {
            handleUnauthorized(error)
        }
    }

    /// Tries the server again now instead of waiting for the next retry.
    func retryConnection() async {
        guard isOffline else { return }
        await refreshUser()
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
        // Offline there's no one to tell; the token is forgotten either way.
        if !isOffline { try? await api.signOut() }
        clearSession()
    }

    func deleteAccount() async throws {
        try await api.deleteAccount()
        clearSession()
    }

    func update(_ user: User) {
        self.user = user
        store.saveUser(user)
    }

    // MARK: Offline

    /// Starts copying the library to the device, unless a copy is underway.
    func syncForOffline() {
        guard !sync.isRunning else { return }
        sync.start(api: api, store: store)
    }

    /// Removes saved pins, their pieces, shopping links and images from this
    /// device. The server keeps everything and the user stays signed in.
    func clearLocalData() {
        sync.cancel()
        store.clearPinData()
        URLCache.shared.removeAllCachedResponses()
        localDataResetCount += 1
    }

    private func setReachable(_ reachable: Bool) {
        guard reachable == isOffline else { return }
        isOffline = !reachable
        if reachable {
            retryTask?.cancel()
            retryTask = nil
            reconnectCount += 1
        } else {
            sync.cancel()
            startRetrying()
        }
    }

    /// Checks back with the server, backing off while it stays down.
    private func startRetrying() {
        retryTask?.cancel()
        retryTask = Task { [weak self] in
            var delay = Self.firstRetryDelay
            while !Task.isCancelled {
                try? await Task.sleep(for: delay)
                guard !Task.isCancelled, let self, self.isOffline, self.token != nil else { return }
                await self.refreshUser()
                delay = min(delay * 2, Self.maxRetryDelay)
            }
        }
    }

    /// Signs out when the server rejects the session; returns true if it did.
    @discardableResult
    func handleUnauthorized(_ error: Error) -> Bool {
        guard case APIError.unauthorized = error else { return false }
        clearSession()
        return true
    }

    private func startSession(_ response: AuthResponse) {
        // Never show one account's pins to another.
        if store.loadUser()?.id != response.user.id { store.clearAll() }
        token = response.token
        tokenStore.save(response.token)
        update(response.user)
        phase = .signedIn
    }

    private func clearSession() {
        token = nil
        user = nil
        tokenStore.clear()
        sync.cancel()
        retryTask?.cancel()
        retryTask = nil
        isOffline = false
        store.clearAll()
        URLCache.shared.removeAllCachedResponses()
        isOnboardingPresented = false
        phase = .signedOut
    }
}
