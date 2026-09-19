import SwiftUI

struct LookOrderView: View {
    var onChange: (Look) -> Void

    @Environment(AppModel.self) private var app
    @State private var look: Look
    @State private var statusEdits: [String: LookStore.Status] = [:]
    @State private var orderRefEdits: [String: String] = [:]
    @State private var trackingNoEdits: [String: String] = [:]
    @State private var carrierEdits: [String: String] = [:]
    @State private var savingMerchant: String?
    @State private var errorMessage: String?

    init(look: Look, onChange: @escaping (Look) -> Void = { _ in }) {
        self.onChange = onChange
        _look = State(initialValue: look)
        var status: [String: LookStore.Status] = [:]
        var orderRef: [String: String] = [:]
        var trackingNo: [String: String] = [:]
        var carrier: [String: String] = [:]
        for store in look.stores {
            status[store.merchant] = store.status
            orderRef[store.merchant] = store.orderRef
            trackingNo[store.merchant] = store.trackingNo
            carrier[store.merchant] = store.carrier
        }
        _statusEdits = State(initialValue: status)
        _orderRefEdits = State(initialValue: orderRef)
        _trackingNoEdits = State(initialValue: trackingNo)
        _carrierEdits = State(initialValue: carrier)
    }

    var body: some View {
        List {
            ForEach(look.plan.stores) { store in
                Section(store.name) {
                    ForEach(store.items) { item in
                        ItemRow(item: item)
                    }

                    if let address = store.address {
                        Text(address.summary)
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }

                    Picker("Status", selection: statusBinding(store.merchant)) {
                        ForEach(LookStore.Status.allCases, id: \.self) { status in
                            Text(status.title).tag(status)
                        }
                    }
                    TextField("Order number", text: orderRefBinding(store.merchant))
                    Picker("Carrier", selection: carrierBinding(store.merchant)) {
                        Text("None").tag("")
                        ForEach(Carrier.allCases) { carrier in
                            Text(carrier.title).tag(carrier.rawValue)
                        }
                    }
                    TextField("Tracking number", text: trackingNoBinding(store.merchant))

                    if savingMerchant == store.merchant {
                        ProgressView()
                    } else {
                        Button("Save") { save(store.merchant) }
                            .accessibilityIdentifier("orders.save.\(store.merchant)")
                    }

                    if let trackingUrl = look.store(store.merchant)?.trackingUrl, let url = URL(string: trackingUrl) {
                        Button("Track Package") { SafariPresenter.open(url) }
                    }

                    if look.store(store.merchant)?.status == .pending, let url = URL(string: store.checkoutUrl) {
                        Button("Open Checkout") { SafariPresenter.open(url) }
                    }

                    if let errorMessage, savingMerchant == nil {
                        Text(errorMessage).foregroundStyle(.red)
                    }
                }
            }
        }
        .navigationTitle(look.pinTitle.isEmpty ? "Order" : look.pinTitle)
        .navigationBarTitleDisplayMode(.inline)
    }

    private func statusBinding(_ merchant: String) -> Binding<LookStore.Status> {
        Binding(get: { statusEdits[merchant] ?? .pending }, set: { statusEdits[merchant] = $0 })
    }

    private func orderRefBinding(_ merchant: String) -> Binding<String> {
        Binding(get: { orderRefEdits[merchant] ?? "" }, set: { orderRefEdits[merchant] = $0 })
    }

    private func trackingNoBinding(_ merchant: String) -> Binding<String> {
        Binding(get: { trackingNoEdits[merchant] ?? "" }, set: { trackingNoEdits[merchant] = $0 })
    }

    private func carrierBinding(_ merchant: String) -> Binding<String> {
        Binding(get: { carrierEdits[merchant] ?? "" }, set: { carrierEdits[merchant] = $0 })
    }

    private func save(_ merchant: String) {
        savingMerchant = merchant
        let update = LookStoreUpdate(
            status: statusEdits[merchant] ?? .pending,
            orderRef: (orderRefEdits[merchant] ?? "").trimmingCharacters(in: .whitespaces),
            trackingNo: (trackingNoEdits[merchant] ?? "").trimmingCharacters(in: .whitespaces),
            carrier: carrierEdits[merchant] ?? ""
        )
        Task {
            defer { savingMerchant = nil }
            do {
                let updated = try await app.api.updateLookStore(lookID: look.id, merchant: merchant, update)
                if let index = look.stores.firstIndex(where: { $0.merchant == merchant }) {
                    look.stores[index] = updated
                } else {
                    look.stores.append(updated)
                }
                statusEdits[merchant] = updated.status
                orderRefEdits[merchant] = updated.orderRef
                trackingNoEdits[merchant] = updated.trackingNo
                carrierEdits[merchant] = updated.carrier
                errorMessage = nil
                onChange(look)
            } catch {
                if !app.handleUnauthorized(error) { errorMessage = error.localizedDescription }
            }
        }
    }
}

private struct ItemRow: View {
    let item: CheckoutPlan.Item

    var body: some View {
        HStack {
            Text(item.label)
            Spacer()
            if let variantTitle = item.variantTitle, !variantTitle.isEmpty {
                Text(variantTitle)
                    .foregroundStyle(.secondary)
            } else if let size = item.size, !size.isEmpty {
                Text(size)
                    .foregroundStyle(.secondary)
            }
            if let price = item.price {
                Text(price.formatted)
                    .foregroundStyle(.secondary)
            }
        }
        .font(.subheadline)
    }
}
