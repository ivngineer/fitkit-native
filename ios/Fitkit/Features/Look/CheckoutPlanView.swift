import SwiftUI

/// Shows what each store in the plan will charge and ship, and hands off to
/// each store's own checkout in Safari. Fitkit never touches payment.
struct CheckoutPlanView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var look: Look

    @State private var openedMerchant: String?
    @State private var showOrderedAlert = false
    @State private var errorMessage: String?

    init(look: Look) {
        _look = State(initialValue: look)
    }

    var body: some View {
        NavigationStack {
            List {
                Section {
                    HStack {
                        Text(look.plan.stores.count == 1 ? "1 store" : "\(look.plan.stores.count) stores")
                        Spacer()
                        Text(look.plan.totals.map(\.formatted).joined(separator: " + "))
                            .fontWeight(.semibold)
                    }
                    ForEach(look.plan.warnings, id: \.self) { warning in
                        WarningRow(text: warning)
                    }
                }

                ForEach(look.plan.stores) { store in
                    StoreSection(
                        store: store,
                        status: look.store(store.merchant)?.status,
                        openURL: { url in
                            openedMerchant = store.merchant
                            SafariPresenter.open(url, onFinish: handleReturnFromStore)
                        }
                    )
                }

                if let errorMessage {
                    Section {
                        Text(errorMessage).foregroundStyle(.red)
                    }
                }

                Section {
                    Text(look.plan.disclosure)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            .navigationTitle("Checkout")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                }
            }
        }
        // Yes or no only: an alert with a text field dismisses this sheet on
        // iPad, so order numbers are added under Orders instead.
        .alert("Did you place the order at \(openedStoreName)?", isPresented: $showOrderedAlert) {
            Button("Yes, Ordered") { markOrdered() }
            Button("Not Yet", role: .cancel) {}
        } message: {
            Text("You can add the order and tracking numbers later under Account → Orders.")
        }
    }

    private var openedStoreName: String {
        look.plan.stores.first { $0.merchant == openedMerchant }?.name ?? ""
    }

    private func handleReturnFromStore() {
        guard let merchant = openedMerchant,
              look.store(merchant)?.status ?? .pending == .pending else { return }
        showOrderedAlert = true
    }

    private func markOrdered() {
        guard let merchant = openedMerchant else { return }
        Task {
            do {
                let update = LookStoreUpdate(status: .ordered)
                let updated = try await app.api.updateLookStore(lookID: look.id, merchant: merchant, update)
                applyUpdate(updated, merchant: merchant)
            } catch {
                if !app.handleUnauthorized(error) { errorMessage = error.localizedDescription }
            }
        }
    }

    private func applyUpdate(_ updated: LookStore, merchant: String) {
        if let index = look.stores.firstIndex(where: { $0.merchant == merchant }) {
            look.stores[index] = updated
        } else {
            look.stores.append(updated)
        }
    }
}

private struct WarningRow: View {
    let text: String

    var body: some View {
        Label {
            Text(text)
        } icon: {
            Image(systemName: "exclamationmark.triangle")
        }
        .font(.caption)
        .foregroundStyle(.orange)
    }
}

private struct StoreSection: View {
    let store: CheckoutPlan.Store
    let status: LookStore.Status?
    var openURL: (URL) -> Void

    var body: some View {
        Section {
            ForEach(store.items) { item in
                ItemRow(item: item)
            }

            HStack {
                Text("Subtotal")
                Spacer()
                Text(store.subtotals.map(\.formatted).joined(separator: " + "))
            }
            .font(.subheadline)
            .foregroundStyle(.secondary)

            if let note = store.note {
                Text(note).font(.caption).foregroundStyle(.secondary)
            }

            ForEach(store.warnings, id: \.self) { warning in
                WarningRow(text: warning)
            }

            if let address = store.address {
                AddressRow(summary: address.summary, prefilled: store.prefilled)
            }

            Button(primaryTitle) {
                if let url = URL(string: store.checkoutUrl) { openURL(url) }
            }
            .accessibilityIdentifier("look.checkout.\(store.merchant)")

            ForEach(store.separateItems) { item in
                Button("Open \(item.label)") {
                    if let url = URL(string: item.openUrl) { openURL(url) }
                }
                .font(.subheadline)
            }
        } header: {
            HStack {
                Text(store.name)
                Spacer()
                if let status {
                    Text(status.title)
                        .font(.caption2)
                        .textCase(nil)
                }
            }
        }
    }

    private var primaryTitle: String {
        let count = store.items.filter(\.inCart).count
        if store.method.isCart {
            return "Checkout at \(store.name) (\(count) item\(count == 1 ? "" : "s"))"
        }
        return "Open at \(store.name)"
    }
}

private struct AddressRow: View {
    let summary: String
    let prefilled: Bool

    var body: some View {
        HStack {
            Text(summary)
                .font(.footnote)
                .foregroundStyle(.secondary)
            Spacer()
            if prefilled {
                Text("Filled in at checkout")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            } else {
                Button {
                    UIPasteboard.general.string = summary
                } label: {
                    Image(systemName: "doc.on.doc")
                }
                .font(.caption)
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
            if item.sizeAtCheckout {
                Text("Size at checkout").foregroundStyle(.secondary)
            } else if let variantTitle = item.variantTitle, !variantTitle.isEmpty {
                Text(variantTitle).foregroundStyle(.secondary)
            } else if let size = item.size, !size.isEmpty {
                Text(size).foregroundStyle(.secondary)
            }
            if let price = item.price {
                Text(price.formatted).foregroundStyle(.secondary)
            }
        }
        .font(.subheadline)
    }
}
