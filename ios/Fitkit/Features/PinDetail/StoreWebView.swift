import SwiftUI
import WebKit

/// A store page shown inside the iPad side panel, pushed onto the panel's
/// navigation stack so the back button returns to the pieces.
struct StoreWebView: View {
    let url: URL
    @Environment(\.openURL) private var openURL
    @State private var title = ""
    @State private var progress = 0.0
    @State private var currentURL: URL?

    var body: some View {
        WebView(url: url, title: $title, progress: $progress, currentURL: $currentURL)
            .ignoresSafeArea(edges: .bottom)
            .overlay(alignment: .top) {
                if progress < 1 {
                    ProgressView(value: progress)
                        .progressViewStyle(.linear)
                        .transition(.opacity)
                }
            }
            .animation(.default, value: progress < 1)
            .navigationTitle(title.isEmpty ? (url.host() ?? "") : title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    ShareLink(item: currentURL ?? url)
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        openURL(currentURL ?? url)
                    } label: {
                        Label("Open in Safari", systemImage: "safari")
                    }
                    .accessibilityIdentifier("store.openInSafari")
                }
            }
            .accessibilityIdentifier("store.web")
    }
}

private struct WebView: UIViewRepresentable {
    let url: URL
    @Binding var title: String
    @Binding var progress: Double
    @Binding var currentURL: URL?

    func makeUIView(context: Context) -> WKWebView {
        let view = WKWebView()
        view.allowsBackForwardNavigationGestures = true
        context.coordinator.observe(view)
        view.load(URLRequest(url: url))
        return view
    }

    func updateUIView(_ view: WKWebView, context: Context) {}

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    @MainActor final class Coordinator {
        private let parent: WebView
        private var observations: [NSKeyValueObservation] = []

        init(_ parent: WebView) { self.parent = parent }

        func observe(_ view: WKWebView) {
            observations = [
                view.observe(\.title) { [parent] view, _ in
                    MainActor.assumeIsolated { parent.title = view.title ?? "" }
                },
                view.observe(\.estimatedProgress) { [parent] view, _ in
                    MainActor.assumeIsolated { parent.progress = view.estimatedProgress }
                },
                view.observe(\.url) { [parent] view, _ in
                    MainActor.assumeIsolated { parent.currentURL = view.url }
                },
            ]
        }
    }
}
