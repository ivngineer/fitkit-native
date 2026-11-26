import SwiftUI

/// Screen for picking pieces and sizes, then buying them through Fitkit
/// ("Buy" places an order Fitkit's buyers fulfil) or, as before, building a
/// checkout plan to pay each store directly. Pushed onto the pin's navigation
/// stack, so it closes with the back button or a swipe.
struct LookBuilderView: View {
    @Environment(AppModel.self) private var app
    @State private var model: LookBuilderModel
    @State private var sheet: BuilderSheet?

    init(pin: Pin, result: AnalysisResult) {
        _model = State(initialValue: LookBuilderModel(pin: pin, result: result))
    }

    var body: some View {
        Group {
            if model.phase == .loading {
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                form
            }
        }
        .background(Color.screenGroupedBackground)
        .navigationTitle("Get This Look")
        .navigationBarTitleDisplayMode(.inline)
        .task { await model.load(api: app.api, app: app) }
        // One sheet modifier: a second would dismiss whatever the first shows.
        .sheet(item: $sheet) { sheet in
            switch sheet {
            case .addAddress:
                NavigationStack {
                    AddressEditorView(address: nil) { saved in
                        Task { await model.reloadAddresses(api: app.api, selecting: saved.id) }
                    }
                }
            case .checkout(let look):
                CheckoutPlanView(look: look)
            case .ordered(let order):
                OrderConfirmationView(order: order)
            }
        }
    }

    private var form: some View {
        Form {
            ForEach(model.pieces.indices, id: \.self) { index in
                Section {
                    PieceRow(model: model, index: index)
                }
                .listRowBackground(Color.screenGroupedRow)
            }

            // With one address there's nothing to choose; "Change Address"
            // above the button adds another.
            if model.addresses.count > 1 {
                Section {
                    Picker("Address", selection: $model.addressID) {
                        ForEach(model.addresses) { address in
                            VStack(alignment: .leading) {
                                Text(address.title)
                                Text(address.summary).font(.caption).foregroundStyle(.secondary)
                            }
                            .tag(address.id as String?)
                        }
                    }
                    .accessibilityIdentifier("look.address")
                } header: {
                    Text("Deliver to")
                } footer: {
                    if model.selectedAddress?.country == "UA" {
                        Text("Stores that don't ship to Ukraine go to your NP Shopping address. Add one under Account → Addresses.")
                    }
                }
                .listRowBackground(Color.screenGroupedRow)
            }

            Section {
                TextField("Name or nickname", text: $model.buyerName)
                    .textContentType(.nickname)
                    .autocorrectionDisabled()
                    .submitLabel(.done)
                    .accessibilityIdentifier("look.buyerName")
            } header: {
                Text("Your name")
            } footer: {
                Text("Fitkit's team buys these pieces for you. Follow the order under Orders.")
            }
            .listRowBackground(Color.screenGroupedRow)

            if let errorMessage = model.errorMessage {
                Section {
                    Text(errorMessage).foregroundStyle(.red)
                }
                .listRowBackground(Color.screenGroupedRow)
            }
        }
        .scrollContentBackground(.hidden)
        // Gaps between pieces match the list's side margins.
        .listSectionSpacing(16)
        .contentMargins(.bottom, 128, for: .scrollContent)
        .overlay(alignment: .bottom) { bottomBar }
    }

    /// Same pill and fade as "Get This Look" on the pin screen. "Buy" leads;
    /// paying each store directly stays one tap away.
    private var bottomBar: some View {
        VStack(spacing: 12) {
            Button("Check Out at Each Store Instead", action: checkOutAtStores)
                .font(.footnote)
                .tint(.secondary)
                .buttonStyle(.borderless)
                .disabled(model.isSubmitting)
                .accessibilityIdentifier("look.review")
            buyButton
        }
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

    private var buyButton: some View {
        Button {
            // Not `.disabled`: that dims the white pill; grey text says it instead.
            guard model.canBuy else { return }
            Task {
                if let order = await model.buy(api: app.api, app: app) {
                    sheet = .ordered(order)
                }
            }
        } label: {
            if model.isSubmitting {
                pill(ProgressView().tint(.black))
            } else {
                pill(Text("Buy").foregroundStyle(model.canBuy ? .black : .gray))
            }
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("look.buy")
    }

    /// The earlier checkout: links to each store's own checkout, delivered to
    /// one of the user's addresses (added here first if there is none).
    private func checkOutAtStores() {
        guard !model.addresses.isEmpty else {
            sheet = .addAddress
            return
        }
        guard model.canSubmit else { return }
        Task {
            if let look = await model.submit(api: app.api, app: app) {
                sheet = .checkout(look)
            }
        }
    }

    private func pill(_ content: some View) -> some View {
        content
            .font(.headline)
            .frame(maxWidth: .infinity, minHeight: 56)
            .background(.white, in: .capsule)
            .contentShape(.capsule)
    }
}

private enum BuilderSheet: Identifiable {
    case addAddress
    case checkout(Look)
    case ordered(FitkitOrder)

    var id: String {
        switch self {
        case .addAddress: "address"
        case .checkout(let look): "checkout-" + look.id
        case .ordered(let order): "order-" + order.id
        }
    }
}

/// One piece: a checkbox, the listing's picture, and its name with the size
/// menu beside it. Tapping anywhere but the menu ticks or unticks the piece.
private struct PieceRow: View {
    let model: LookBuilderModel
    let index: Int

    @Environment(AppModel.self) private var app

    private var piece: LookBuilderModel.Piece { model.pieces[index] }

    var body: some View {
        HStack(spacing: 12) {
            Button(action: toggle) {
                Image(systemName: piece.included ? "checkmark.circle.fill" : "circle")
                    .font(.title2)
                    .foregroundStyle(piece.included ? Color.primary : Color.secondary)
            }
            .buttonStyle(.borderless)
            .accessibilityLabel(piece.item.label)
            .accessibilityAddTraits(piece.included ? .isSelected : [])
            .accessibilityIdentifier("look.piece.\(piece.id).include")

            PinImage(url: app.api.resolve(piece.item.cropUrl ?? piece.listing?.thumbnailUrl), emptySymbol: "tag")
                .frame(width: 44, height: 44)
                .clipShape(.rect(cornerRadius: 8))

            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 4) {
                    FadingLine(text: piece.item.label).font(.subheadline)
                    if piece.included { sizeMenu }
                }
                if let listing = piece.listing {
                    Text([listing.merchant, listing.price].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · "))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
        }
        .contentShape(.rect)
        .onTapGesture(perform: toggle)
    }

    private func toggle() {
        withAnimation(.easeInOut(duration: 0.12)) {
            model.pieces[index].included.toggle()
        }
    }

    private var product: ListingOptions.Product? {
        guard let product = model.product(for: piece), product.variants.count > 1 else { return nil }
        return product
    }

    @ViewBuilder private var sizeMenu: some View {
        if let product {
            Menu {
                Picker("Size", selection: variantBinding) {
                    ForEach(product.variants) { variant in
                        Text(variant.available ? variant.title : "\(variant.title) – sold out")
                            .tag(Int64?.some(variant.id))
                            .disabled(!variant.available)
                    }
                }
            } label: {
                sizeLabel(product.variants.first { $0.id == piece.variantID }?.title)
            }
            .buttonStyle(.borderless)
            .tint(.primary)
            .accessibilityIdentifier("look.piece.\(piece.id).size")
        } else if let category = SizeCategory(rawValue: piece.item.category) {
            Menu {
                Picker("Size", selection: sizeBinding) {
                    Text("None").tag("")
                    ForEach(sizeChoices(for: category), id: \.self) { size in
                        Text(size).tag(size)
                    }
                }
            } label: {
                sizeLabel(piece.size.isEmpty ? nil : piece.size)
            }
            .buttonStyle(.borderless)
            .tint(.primary)
            .accessibilityIdentifier("look.piece.\(piece.id).size")
        }
    }

    private func sizeLabel(_ value: String?) -> some View {
        HStack(spacing: 4) {
            Text("Size")
            if let value { Text(value).foregroundStyle(.secondary) }
            Image(systemName: "chevron.up.chevron.down").font(.caption)
        }
        .font(.subheadline)
        .fixedSize()
    }

    /// Common sizes for stores whose sizes Fitkit can't read, plus whatever
    /// the user entered before.
    private func sizeChoices(for category: SizeCategory) -> [String] {
        var sizes = category == .shoes
            ? (35...47).map(String.init)
            : ["XXS", "XS", "S", "M", "L", "XL", "XXL", "3XL"]
        if !piece.size.isEmpty, !sizes.contains(piece.size) { sizes.append(piece.size) }
        return sizes
    }

    private var variantBinding: Binding<Int64?> {
        Binding(get: { model.pieces[index].variantID }, set: { model.pieces[index].variantID = $0 })
    }

    private var sizeBinding: Binding<String> {
        Binding(get: { model.pieces[index].size }, set: { model.pieces[index].size = $0 })
    }
}

/// One line of text that fades out at the end of its space instead of
/// ending in "…".
private struct FadingLine: View {
    let text: String

    var body: some View {
        Text(text)
            .lineLimit(1)
            .fixedSize(horizontal: true, vertical: false)
            .frame(maxWidth: .infinity, alignment: .leading)
            .clipped()
            .mask {
                HStack(spacing: 0) {
                    Rectangle()
                    LinearGradient(colors: [.black, .clear], startPoint: .leading, endPoint: .trailing)
                        .frame(width: 20)
                }
                .padding(.trailing, 4)
            }
    }
}
