import SwiftUI

/// Shown right after "Buy": the order number to quote and what was ordered.
struct OrderConfirmationView: View {
    let order: FitkitOrder

    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                Section {
                    VStack(spacing: 10) {
                        Image(systemName: "checkmark.circle.fill")
                            .font(.system(size: 52))
                            .foregroundStyle(.green)
                        Text("Order \(order.number)")
                            .font(.title2.weight(.semibold))
                            .accessibilityIdentifier("order.number")
                        Text(order.status)
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                        Text("Fitkit's team will buy these pieces for you. You'll find the order under Orders.")
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                            .multilineTextAlignment(.center)
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 8)
                    .accessibilityElement(children: .combine)
                }
                FitkitOrderItemsSection(order: order)
            }
            .listStyle(.insetGrouped)
            .navigationTitle("Order Placed")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                        .accessibilityIdentifier("order.done")
                }
            }
        }
    }
}

/// One Fitkit order in the Orders list.
struct FitkitOrderRow: View {
    let order: FitkitOrder

    @Environment(AppModel.self) private var app

    var body: some View {
        HStack(spacing: 12) {
            PinImage(url: app.api.resolve(order.photoUrl), emptySymbol: "bag")
                .frame(width: 44, height: 56)
                .clipShape(.rect(cornerRadius: 8))
            VStack(alignment: .leading, spacing: 3) {
                Text("Order \(order.number)")
                    .font(.subheadline)
                Text(order.createdAt, format: .dateTime.day().month().year())
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Text(order.status)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: 0)
        }
        .accessibilityElement(children: .combine)
    }
}

/// A Fitkit order's status and pieces.
struct FitkitOrderDetailView: View {
    let order: FitkitOrder

    @Environment(AppModel.self) private var app

    var body: some View {
        List {
            Section {
                HStack(spacing: 14) {
                    PinImage(url: app.api.resolve(order.photoUrl), emptySymbol: "bag")
                        .frame(width: 56, height: 72)
                        .clipShape(.rect(cornerRadius: 10))
                    VStack(alignment: .leading, spacing: 4) {
                        Text(order.status)
                            .font(.headline)
                        Text(order.createdAt, format: .dateTime.day().month().year().hour().minute())
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                    }
                }
                .accessibilityElement(children: .combine)
            }
            FitkitOrderItemsSection(order: order)
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Order \(order.number)")
        .navigationBarTitleDisplayMode(.inline)
    }
}

/// The pieces of an order, with the total when every price is in hryvnias.
private struct FitkitOrderItemsSection: View {
    let order: FitkitOrder

    @Environment(AppModel.self) private var app

    var body: some View {
        Section {
            ForEach(Array(order.items.enumerated()), id: \.offset) { _, item in
                HStack(spacing: 12) {
                    PinImage(url: app.api.resolve(item.photoUrl), emptySymbol: "tag")
                        .frame(width: 44, height: 44)
                        .clipShape(.rect(cornerRadius: 8))
                    VStack(alignment: .leading, spacing: 2) {
                        Text(item.name)
                            .font(.subheadline)
                            .strikethrough(item.cancelled)
                        Text(details(item))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                    Spacer(minLength: 0)
                    if let price = item.displayPrice {
                        Text(price)
                            .font(.subheadline)
                            .monospacedDigit()
                            .foregroundStyle(item.cancelled ? Color.secondary : Color.primary)
                    }
                }
                .opacity(item.cancelled ? 0.6 : 1)
                .accessibilityElement(children: .combine)
            }
        } header: {
            Text(order.items.count == 1 ? "1 piece" : "\(order.items.count) pieces")
        } footer: {
            if let total = order.total {
                Text("Total \(Hryvnia.format(total))")
            }
        }
    }

    private func details(_ item: FitkitOrder.Item) -> String {
        var parts = [item.store, "Size \(item.size)"]
        if item.quantity > 1 { parts.append("× \(item.quantity)") }
        if item.cancelled { parts.append("Not available, refunded") }
        return parts.joined(separator: " · ")
    }
}
