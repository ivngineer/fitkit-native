import SafariServices
import SwiftUI

struct PinDetailView: View {
    @Environment(AppModel.self) private var app
    @Environment(CartModel.self) private var cart
    @Environment(\.dismiss) private var dismiss
    @State private var model: PinDetailModel
    @State private var browsingURL: IdentifiableURL?

    /// Removes the pin from the grid; the sheet is dismissed by the caller.
    var onRemove: () -> Void
    /// Set when the view sits in a side panel rather than a sheet. There's no
    /// swipe to dismiss there, so a close button leads the toolbar instead.
    var onClose: (() -> Void)?

    init(pin: Pin, onClose: (() -> Void)? = nil, onRemove: @escaping () -> Void) {
        _model = State(initialValue: PinDetailModel(pin: pin))
        self.onClose = onClose
        self.onRemove = onRemove
    }

    var body: some View {
        NavigationStack {
            content
                .navigationTitle("Shop This Look")
                .navigationBarTitleDisplayMode(.inline)
                // In the side panel a store page pushes in place, keeping the
                // grid beside it. A sheet covers the screen with Safari.
                .navigationDestination(item: browsingURL(inPanel: true)) { item in
                    StoreWebView(url: item.url)
                }
                .toolbar {
                    if let onClose {
                        ToolbarItem(placement: .topBarLeading) {
                            Button(action: onClose) {
                                Label("Close", systemImage: "xmark")
                            }
                            .keyboardShortcut(.cancelAction)
                            .accessibilityIdentifier("pin.close")
                        }
                    } else {
                        ToolbarItem(placement: .confirmationAction) {
                            Button("Done") { dismiss() }
                        }
                    }
                    ToolbarItem(placement: .topBarLeading) {
                        Menu {
                            Button {
                                toggleCart()
                            } label: {
                                Label(isInCart ? "Remove from Cart" : "Add to Cart",
                                      systemImage: isInCart ? "cart.badge.minus" : "cart.badge.plus")
                            }
                            .disabled(app.isOffline)
                            if case .loaded = model.state {
                                Button {
                                    model.start(api: app.api, app: app, force: true)
                                } label: {
                                    Label("Search Again", systemImage: "arrow.clockwise")
                                }
                                .disabled(app.isOffline)
                            }
                            if let url = URL(string: model.pin.pinterestUrl) {
                                Link(destination: url) {
                                    Label("Open in Pinterest", systemImage: "arrow.up.right.square")
                                }
                            }
                            Divider()
                            Button(role: .destructive, action: onRemove) {
                                Label("Remove from Fitkit", systemImage: "eye.slash")
                            }
                            .disabled(app.isOffline)
                        } label: {
                            Label("More", systemImage: "ellipsis.circle")
                        }
                        .accessibilityIdentifier("pin.menu")
                    }
                }
        }
        .onAppear { model.start(api: app.api, app: app) }
        .onDisappear { model.cancel() }
        .fullScreenCover(item: browsingURL(inPanel: false)) { item in
            SafariView(url: item.url).ignoresSafeArea()
        }
    }

    private var isInCart: Bool { cart.contains(model.pin) }

    private func browsingURL(inPanel: Bool) -> Binding<IdentifiableURL?> {
        Binding {
            (onClose != nil) == inPanel ? browsingURL : nil
        } set: {
            browsingURL = $0
        }
    }

    private func toggleCart() {
        Task { await cart.toggle(model.pin, api: app.api, app: app) }
    }

    @ViewBuilder private var content: some View {
        switch model.state {
        case .loading, .analyzing:
            VStack(spacing: 20) {
                PinImage(url: app.api.resolve(model.pin.imageUrl), dominantColor: model.pin.dominantColor)
                    .aspectRatio(model.pin.aspectRatio, contentMode: .fit)
                    .frame(maxHeight: 180)
                    .clipShape(.rect(cornerRadius: 14))
                ProgressView()
                    .controlSize(.large)
                VStack(spacing: 6) {
                    Text("Finding the pieces…")
                        .font(.headline)
                    Text("Spotting each item and searching stores for matches. This can take up to a minute.")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                }
            }
            .padding(24)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .accessibilityElement(children: .combine)

        case .noItems(let message):
            // Nothing wearable in the picture, so offer to clear it out.
            ContentUnavailableView {
                Label("Nothing to Shop", systemImage: "tshirt")
            } description: {
                Text(message)
            } actions: {
                VStack(spacing: 12) {
                    Button(role: .destructive, action: onRemove) {
                        Label("Remove from Fitkit", systemImage: "eye.slash")
                    }
                    .buttonStyle(.borderedProminent)
                    .accessibilityIdentifier("pin.remove")
                    Button("Search Again") { model.start(api: app.api, app: app, force: true) }
                }
                .disabled(app.isOffline)
            }

        case .offline:
            ContentUnavailableView {
                Label("You're Offline", systemImage: "wifi.slash")
            } description: {
                Text("This pin hasn't been broken down on this device yet. Connect to see its pieces.")
            } actions: {
                Button("Try Again") { model.start(api: app.api, app: app) }
                    .buttonStyle(.borderedProminent)
            }

        case .failed(let message, let canRetry):
            ContentUnavailableView {
                Label("No Results", systemImage: "magnifyingglass")
            } description: {
                Text(message)
            } actions: {
                if canRetry {
                    Button("Try Again") { model.start(api: app.api, app: app, force: true) }
                        .buttonStyle(.borderedProminent)
                        .disabled(app.isOffline)
                }
            }

        case .loaded(let result):
            List {
                Section {
                    PinSummaryRow(pin: model.pin, imageURL: app.api.resolve(model.pin.imageUrl), result: result)
                }

                // One row per piece: the best match, titled with the piece
                // itself rather than the store's long product name.
                Section("The Pieces") {
                    ForEach(result.items) { item in
                        if let listing = item.listings.first {
                            ListingRow(title: item.label, listing: listing,
                                       imageURL: app.api.resolve(item.cropUrl ?? listing.thumbnailUrl)) {
                                if let url = URL(string: listing.url) { browsingURL = IdentifiableURL(url: url) }
                            }
                        } else {
                            UnmatchedItemRow(item: item, cropURL: app.api.resolve(item.cropUrl))
                        }
                    }
                }
            }
            .listStyle(.insetGrouped)
            .contentMargins(.bottom, 96, for: .scrollContent)
            .overlay(alignment: .bottom) { buyOutfitBar(total: result.outfitTotal) }
        }
    }

    /// Checkout isn't wired up yet. The button is here so the layout settles
    /// around it before one-tap buy and ship arrives.
    ///
    /// The pill sits low, inset about as far from the sides as from the bottom
    /// so it follows the rounded screen corners, over a fade to dark that
    /// keeps it readable above the list.
    private func buyOutfitBar(total: String?) -> some View {
        Button {
        } label: {
            HStack(spacing: 8) {
                Text("Buy Outfit")
                if let total {
                    Text("·").foregroundStyle(.black.opacity(0.35))
                    Text(total).monospacedDigit()
                }
            }
            .font(.headline)
            .foregroundStyle(.black)
            .frame(maxWidth: .infinity, minHeight: 56)
            .background(.white, in: .capsule)
            .contentShape(.capsule)
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("pin.buyOutfit")
        .padding(.horizontal, 22)
        .padding(.bottom, 20)
        .padding(.top, 48)
        .background {
            LinearGradient(colors: [.black.opacity(0), .black.opacity(0.55), .black.opacity(0.9)],
                           startPoint: .top, endPoint: .bottom)
                .allowsHitTesting(false)
        }
        .ignoresSafeArea(edges: .bottom)
    }
}

private struct PinSummaryRow: View {
    let pin: Pin
    let imageURL: URL?
    let result: AnalysisResult

    var body: some View {
        HStack(spacing: 14) {
            PinImage(url: imageURL, dominantColor: pin.dominantColor)
                .frame(width: 56, height: 72)
                .clipShape(.rect(cornerRadius: 10))
            VStack(alignment: .leading, spacing: 4) {
                Text(pin.title)
                    .font(.headline)
                    .lineLimit(2)
                Text(result.items.count == 1 ? "1 item found" : "\(result.items.count) items found")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                if result.demo {
                    Label("Demo results", systemImage: "flask")
                        .font(.caption.weight(.medium))
                        .foregroundStyle(.orange)
                }
            }
        }
        .accessibilityElement(children: .combine)
    }
}

/// A piece we spotted but couldn't match to a store.
private struct UnmatchedItemRow: View {
    let item: DetectedItem
    let cropURL: URL?

    var body: some View {
        HStack(spacing: 12) {
            Group {
                if let cropURL {
                    PinImage(url: cropURL)
                } else {
                    Image(systemName: item.symbolName)
                        .font(.title3)
                        .foregroundStyle(.tint)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                        .background(.tint.opacity(0.12))
                }
            }
            .frame(width: 52, height: 52)
            .clipShape(.rect(cornerRadius: 10))

            VStack(alignment: .leading, spacing: 3) {
                Text(item.label)
                    .font(.subheadline)
                    .foregroundStyle(.primary)
                Text(item.error ?? "No match in stores yet.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
            Spacer(minLength: 0)
        }
        .accessibilityElement(children: .combine)
    }
}

struct ListingRow: View {
    /// Shown instead of the store's product name, which tends to be a keyword
    /// salad. The rest of the listing - store, price, link - stays as is.
    let title: String
    let listing: Listing
    let imageURL: URL?
    var onOpen: () -> Void

    var body: some View {
        Button(action: onOpen) {
            HStack(spacing: 12) {
                PinImage(url: imageURL, emptySymbol: "bag")
                    .frame(width: 52, height: 52)
                    .clipShape(.rect(cornerRadius: 8))

                VStack(alignment: .leading, spacing: 3) {
                    Text(title)
                        .font(.subheadline)
                        .foregroundStyle(.primary)
                        .lineLimit(2)
                    HStack(spacing: 4) {
                        Text(listing.merchant)
                        if listing.inStock == false {
                            Text("· Out of stock")
                        }
                    }
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                }

                Spacer(minLength: 8)

                if let price = listing.price, !price.isEmpty {
                    Text(price)
                        .font(.subheadline.weight(.semibold))
                        .monospacedDigit()
                        .foregroundStyle(.primary)
                }
                Image(systemName: "chevron.forward")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(.tertiary)
            }
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .contextMenu {
            if let url = URL(string: listing.url) {
                Button { onOpen() } label: { Label("Open", systemImage: "safari") }
                ShareLink(item: url)
                Button {
                    UIPasteboard.general.url = url
                } label: {
                    Label("Copy Link", systemImage: "doc.on.doc")
                }
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityHint("Opens the store page")
    }
}

struct IdentifiableURL: Identifiable, Hashable {
    let url: URL
    var id: String { url.absoluteString }
}

struct SafariView: UIViewControllerRepresentable {
    let url: URL

    func makeUIViewController(context: Context) -> SFSafariViewController {
        SFSafariViewController(url: url)
    }

    func updateUIViewController(_ controller: SFSafariViewController, context: Context) {}
}
