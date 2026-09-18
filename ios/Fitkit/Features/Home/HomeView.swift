import SwiftUI

struct HomeView: View {
    @Environment(AppModel.self) private var app
    @Environment(CartModel.self) private var cart
    @Environment(\.horizontalSizeClass) private var horizontalSizeClass
    @State private var model = HomeModel()
    @State private var selectedPin: Pin?
    /// Mirrors `HomeLayout.usesPanel` for the sheet binding, which can be
    /// called with a stale layout while the window rotates.
    @State private var usesPanel = false
    @State private var isSettingsPresented = false
    @State private var isConnectPresented = false
    @State private var isCartPresented = false
    @State private var isRemovalNoticeVisible = false
    /// The explainer is only worth showing the first couple of removals.
    @AppStorage("removalNoticeShownCount") private var removalNoticeShownCount = 0

    var body: some View {
        GeometryReader { window in
            let layout = HomeLayout(size: window.size, isRegularWidth: horizontalSizeClass == .regular)
            HStack(spacing: 0) {
                home(layout)
                    .frame(width: layout.homeWidth(isPanelOpen: selectedPin != nil))
                if layout.usesPanel, let pin = selectedPin {
                    PinDetailView(pin: pin, onClose: {
                        withAnimation(HomeLayout.panelAnimation) { selectedPin = nil }
                    }, onRemove: {
                        withAnimation(HomeLayout.panelAnimation) { selectedPin = nil }
                        remove(pin)
                    })
                    .id(pin.id)
                    .frame(width: layout.panelWidth)
                    .overlay(alignment: .leading) {
                        Rectangle().fill(Color(.separator)).frame(width: 0.5).ignoresSafeArea()
                    }
                    .transition(.move(edge: .trailing))
                }
            }
            .onChange(of: layout.usesPanel, initial: true) { usesPanel = layout.usesPanel }
        }
        // Runs at launch and again whenever the server comes back.
        .task(id: app.reconnectCount) { await model.load(api: app.api, app: app) }
        .task(id: app.reconnectCount) { await cart.load(api: app.api, app: app) }
        .onChange(of: app.localDataResetCount) {
            model.reset()
            cart.clear()
            Task {
                await model.load(api: app.api, app: app)
                await cart.load(api: app.api, app: app)
            }
        }
        .onDisappear { model.stopWatching() }
    }

    private func home(_ layout: HomeLayout) -> some View {
        NavigationStack {
            GeometryReader { geometry in
                ScrollView {
                    VStack(spacing: 16) {
                        if app.isOffline {
                            OfflineBanner()
                                .transition(.move(edge: .top).combined(with: .opacity))
                        }

                        if let job = model.visibleImportJob {
                            ImportStatusCard(job: job) {
                                Task { await model.startImport(api: app.api, app: app) }
                            } onDismiss: {
                                withAnimation { model.dismissedJobID = job.id }
                            }
                            .transition(.move(edge: .top).combined(with: .opacity))
                        }

                        if let importError = model.importError {
                            Label(importError, systemImage: "exclamationmark.circle")
                                .font(.footnote)
                                .foregroundStyle(.red)
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }

                        MasonryGrid(pins: model.pins,
                                    columnCount: layout.columnCount(forWidth: geometry.size.width),
                                    spacing: layout.gridSpacing) { pin in
                            Button {
                                select(pin, layout: layout)
                            } label: {
                                PinCell(pin: pin, imageURL: app.api.resolve(pin.imageUrl),
                                        isSelected: layout.usesPanel && selectedPin?.id == pin.id)
                            }
                            .buttonStyle(PinButtonStyle())
                            .contextMenu {
                                if let url = URL(string: pin.pinterestUrl) {
                                    Link(destination: url) {
                                        Label("Open in Pinterest", systemImage: "arrow.up.right.square")
                                    }
                                    ShareLink(item: url)
                                }
                                Button {
                                    toggleCart(pin)
                                } label: {
                                    Label(cart.contains(pin) ? "Remove from Cart" : "Add to Cart",
                                          systemImage: cart.contains(pin) ? "cart.badge.minus" : "cart.badge.plus")
                                }
                                .disabled(app.isOffline)
                                Divider()
                                Button(role: .destructive) {
                                    remove(pin)
                                } label: {
                                    Label("Remove from Fitkit", systemImage: "eye.slash")
                                }
                                .disabled(app.isOffline)
                            }
                            .task { await model.loadMoreIfNeeded(after: pin, api: app.api, app: app) }
                            .accessibilityIdentifier("pin.\(pin.id)")
                        }
                        .animation(.default, value: model.pins)

                        if model.isLoadingMore {
                            ProgressView().padding()
                        }
                    }
                    .padding(.horizontal, layout.gridPadding)
                    .padding(.bottom, 24)
                    .frame(width: layout.gridWidth)
                    .frame(maxWidth: .infinity)
                    .animation(.default, value: model.visibleImportJob?.id)
                    .animation(.default, value: app.isOffline)
                }
            }
            .overlay { emptyState }
            .overlay(alignment: .bottom) { removalNotice }
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { toolbar }
            .refreshable {
                await app.retryConnection()
                await model.refreshPins(api: app.api, app: app)
            }
            .sheet(item: sheetPin(layout)) { pin in
                PinDetailView(pin: pin, onRemove: {
                    selectedPin = nil
                    remove(pin)
                })
                    .presentationDetents([.medium, .large])
                    .presentationDragIndicator(.visible)
            }
            .sheet(isPresented: $isCartPresented) {
                CartView()
                    .presentationDetents([.medium, .large])
                    .presentationDragIndicator(.visible)
            }
            .sheet(isPresented: $isSettingsPresented) {
                SettingsView {
                    Task { await model.startImport(api: app.api, app: app) }
                }
            }
            .sheet(isPresented: $isConnectPresented) {
                NavigationStack {
                    PinterestHandleForm(actionTitle: "Import My Saves") {
                        isConnectPresented = false
                        Task { await model.startImport(api: app.api, app: app) }
                    }
                    .toolbar {
                        ToolbarItem(placement: .cancellationAction) {
                            Button("Cancel") { isConnectPresented = false }
                        }
                    }
                }
            }
        }
    }

    /// Opening the panel slides the grid over; swapping pins while it's
    /// open just replaces what the panel shows.
    private func select(_ pin: Pin, layout: HomeLayout) {
        if layout.usesPanel, selectedPin == nil {
            withAnimation(HomeLayout.panelAnimation) { selectedPin = pin }
        } else {
            selectedPin = pin
        }
    }

    /// The selection outlives rotation: in landscape the pin moves from the
    /// sheet to the panel, and back again in portrait.
    private func sheetPin(_ layout: HomeLayout) -> Binding<Pin?> {
        Binding {
            layout.usesPanel ? nil : selectedPin
        } set: { pin in
            // Rotating into the panel layout dismisses the sheet; that
            // shouldn't close the pin.
            if !usesPanel { selectedPin = pin }
        }
    }

    private func remove(_ pin: Pin) {
        Task { await model.remove(pin, api: app.api, app: app) }
        guard removalNoticeShownCount < 2 else { return }
        removalNoticeShownCount += 1
        withAnimation { isRemovalNoticeVisible = true }
    }

    private func toggleCart(_ pin: Pin) {
        Task { await cart.toggle(pin, api: app.api, app: app) }
    }

    @ViewBuilder private var removalNotice: some View {
        if isRemovalNoticeVisible {
            RemovalNotice { withAnimation { isRemovalNoticeVisible = false } }
                .transition(.move(edge: .bottom).combined(with: .opacity))
        }
    }

    @ToolbarContentBuilder private var toolbar: some ToolbarContent {
        // One row: the wordmark on the left, the three actions on the right.
        if #available(iOS 26.0, *) {
            // The wordmark reads as a title, not a button, so drop the glass.
            ToolbarItem(placement: .topBarLeading) { wordmark }
                .sharedBackgroundVisibility(.hidden)
        } else {
            ToolbarItem(placement: .topBarLeading) { wordmark }
        }
        ToolbarItem(placement: .topBarTrailing) {
            Button {
                Task { await model.startImport(api: app.api, app: app) }
            } label: {
                Label("Import from Pinterest", systemImage: "arrow.triangle.2.circlepath")
            }
            .disabled(app.isOffline || app.user?.pinterestUsername == nil || model.importJob?.isActive == true)
        }
        ToolbarItem(placement: .topBarTrailing) {
            Button {
                isCartPresented = true
            } label: {
                cartLabel
            }
            .accessibilityLabel(cart.count == 0 ? "Cart" : "Cart, \(cart.count) pins")
            .accessibilityIdentifier("home.cart")
        }
        ToolbarItem(placement: .topBarTrailing) {
            Button {
                isSettingsPresented = true
            } label: {
                Label("Account", systemImage: "person.crop.circle")
            }
            .accessibilityIdentifier("home.account")
        }
    }

    private var wordmark: some View {
        Text("Fitkit")
            .font(.newsreader(.bold, size: 30, relativeTo: .title))
            .foregroundStyle(.primary)
            // Without this the toolbar squeezes the wordmark down to "F…".
            .fixedSize()
            .accessibilityAddTraits(.isHeader)
    }

    /// The count rides beside the icon: a badge pinned outside the button's
    /// bounds gets clipped by the toolbar.
    private var cartLabel: some View {
        HStack(spacing: 3) {
            Image(systemName: cart.count > 0 ? "cart.fill" : "cart")
            if cart.count > 0 {
                Text(cart.count, format: .number)
                    .font(.footnote.weight(.semibold))
                    .monospacedDigit()
            }
        }
        .accessibilityHidden(true)
    }

    @ViewBuilder private var emptyState: some View {
        if !model.hasLoaded {
            ProgressView()
        } else if model.pins.isEmpty {
            if let error = model.loadError {
                ContentUnavailableView {
                    Label("Couldn't Load Pins", systemImage: "wifi.exclamationmark")
                } description: {
                    Text(error)
                } actions: {
                    Button("Try Again") {
                        Task {
                            await app.retryConnection()
                            await model.refreshPins(api: app.api, app: app)
                        }
                    }
                    .buttonStyle(.bordered)
                }
            } else if app.user?.pinterestUsername == nil {
                ContentUnavailableView {
                    Label("Connect Pinterest", systemImage: "pin")
                } description: {
                    Text("Add your Pinterest username and we'll bring in the outfits you've saved.")
                } actions: {
                    Button("Connect Pinterest") { isConnectPresented = true }
                        .buttonStyle(.borderedProminent)
                        .disabled(app.isOffline)
                        .accessibilityIdentifier("home.connect")
                }
            } else if model.importJob?.isActive != true, model.visibleImportJob == nil {
                ContentUnavailableView {
                    Label("No Saved Pins Yet", systemImage: "square.grid.2x2")
                } description: {
                    Text("Save outfits on Pinterest, then import them here.")
                } actions: {
                    Button("Import from Pinterest") { Task { await model.startImport(api: app.api, app: app) } }
                        .buttonStyle(.borderedProminent)
                        .disabled(app.isOffline)
                }
            }
        }
    }
}

/// How Home arranges itself for the window it's in. Compact widths (iPhone,
/// narrow iPad windows) keep the phone layout. Regular widths get three
/// columns, and landscape ones show a pin in a side panel instead of a sheet.
struct HomeLayout: Equatable {
    var size: CGSize
    var isRegularWidth: Bool

    static let panelAnimation = Animation.smooth(duration: 0.35)
    static let regularColumnCount = 3

    var panelWidth: CGFloat { min(max(size.width * 0.4, 380), 520) }

    /// Landscape, and wide enough that the grid beside the panel still has
    /// comfortable columns.
    var usesPanel: Bool {
        isRegularWidth && size.width > size.height && size.width - panelWidth >= 600
    }

    /// The grid keeps the width it has beside the panel, centered while the
    /// panel is closed, so opening it slides the grid rather than reflowing it.
    var gridWidth: CGFloat? { usesPanel ? size.width - panelWidth : nil }

    func homeWidth(isPanelOpen: Bool) -> CGFloat {
        usesPanel && isPanelOpen ? size.width - panelWidth : size.width
    }

    func columnCount(forWidth width: CGFloat) -> Int {
        isRegularWidth ? Self.regularColumnCount : MasonryLayout.columnCount(for: width)
    }

    var gridPadding: CGFloat { isRegularWidth ? 20 : 12 }
    var gridSpacing: CGFloat { isRegularWidth ? 14 : 10 }
}

/// Says why buttons are greyed out while the server can't be reached.
private struct OfflineBanner: View {
    var body: some View {
        Label("Offline. Showing pins saved on this device.", systemImage: "wifi.slash")
            .font(.footnote.weight(.medium))
            .foregroundStyle(.secondary)
            .padding(.horizontal, 14)
            .padding(.vertical, 8)
            .background(.fill.tertiary, in: .capsule)
            .frame(maxWidth: .infinity)
            .accessibilityIdentifier("home.offline")
    }
}

private struct PinButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .scaleEffect(configuration.isPressed ? 0.97 : 1)
            .animation(.spring(duration: 0.25), value: configuration.isPressed)
    }
}
