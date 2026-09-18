import SwiftUI

@main
struct FitkitApp: App {
    @State private var model = AppModel()
    @State private var cart = CartModel()
    @Environment(\.scenePhase) private var scenePhase

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(model)
                .environment(cart)
                .task { await model.bootstrap() }
        }
        // Coming back to the app is a good moment to look for the server.
        .onChange(of: scenePhase) { _, phase in
            if phase == .active { Task { await model.retryConnection() } }
        }
    }
}

struct RootView: View {
    @Environment(AppModel.self) private var model
    @Environment(CartModel.self) private var cart

    var body: some View {
        Group {
            switch model.phase {
            case .launching:
                ProgressView()
                    .controlSize(.large)
            case .unreachable(let message):
                ContentUnavailableView {
                    Label("Can't Connect", systemImage: "wifi.exclamationmark")
                } description: {
                    Text(message)
                } actions: {
                    Button("Try Again") { Task { await model.bootstrap() } }
                        .buttonStyle(.borderedProminent)
                }
            case .signedOut:
                AuthView()
                    .transition(.opacity)
                    .onAppear { cart.clear() }
            case .signedIn where model.isOnboardingPresented:
                // A full screen step rather than a sheet: the system's
                // Save Password prompt appears right after sign-up and
                // stacking it over a sheet breaks later presentations.
                OnboardingView()
                    .transition(.move(edge: .trailing))
            case .signedIn:
                HomeView()
                    .transition(.opacity)
            }
        }
        .animation(.default, value: model.phase)
        .animation(.default, value: model.isOnboardingPresented)
    }
}
