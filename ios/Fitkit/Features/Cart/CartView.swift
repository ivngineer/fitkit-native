import SwiftUI

/// The cart sheet. It opens at half height and pulls up to full screen when
/// there's more in it than fits.
struct CartView: View {
    @Environment(AppModel.self) private var app
    @Environment(CartModel.self) private var cart
    @Environment(\.dismiss) private var dismiss
    @State private var selectedPin: Pin?

    var body: some View {
        NavigationStack {
            Group {
                if cart.pins.isEmpty {
                    ContentUnavailableView {
                        Label("Cart Is Empty", systemImage: "cart")
                    } description: {
                        Text("Press and hold a pin, then Add to Cart, to keep it here for checkout.")
                    }
                } else {
                    List {
                        Section {
                            ForEach(cart.pins) { pin in
                                Button {
                                    selectedPin = pin
                                } label: {
                                    CartRow(pin: pin, imageURL: app.api.resolve(pin.imageUrl))
                                }
                                .buttonStyle(.plain)
                                .swipeActions {
                                    Button("Remove", systemImage: "trash", role: .destructive) { toggle(pin) }
                                }
                                .contextMenu {
                                    if let url = URL(string: pin.pinterestUrl) {
                                        Link(destination: url) {
                                            Label("Open in Pinterest", systemImage: "arrow.up.right.square")
                                        }
                                    }
                                    Button(role: .destructive) { toggle(pin) } label: {
                                        Label("Remove from Cart", systemImage: "cart.badge.minus")
                                    }
                                }
                            }
                        } footer: {
                            Text("One-tap buy and ship is coming soon. For now the cart keeps everything you want to order in one place.")
                        }
                    }
                    .listStyle(.insetGrouped)
                }
            }
            .navigationTitle("Cart")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                }
            }
            .overlay(alignment: .bottom) {
                if let error = cart.error {
                    Label(error, systemImage: "exclamationmark.circle")
                        .font(.footnote)
                        .foregroundStyle(.red)
                        .padding()
                }
            }
        }
        .task { await cart.load(api: app.api, app: app) }
        // Tapping a row opens the same breakdown the grid opens.
        .sheet(item: $selectedPin) { pin in
            PinDetailView(pin: pin) {
                selectedPin = nil
                Task { await cart.removeFromFitkit(pin, api: app.api, app: app) }
            }
            .presentationDetents([.medium, .large])
            .presentationDragIndicator(.visible)
        }
    }

    private func toggle(_ pin: Pin) {
        Task { await cart.toggle(pin, api: app.api, app: app) }
    }
}

private struct CartRow: View {
    let pin: Pin
    let imageURL: URL?

    var body: some View {
        HStack(spacing: 12) {
            PinImage(url: imageURL, dominantColor: pin.dominantColor)
                .frame(width: 48, height: 62)
                .clipShape(.rect(cornerRadius: 8))
            VStack(alignment: .leading, spacing: 3) {
                Text(pin.title.isEmpty ? "Saved pin" : pin.title)
                    .font(.subheadline)
                    .lineLimit(2)
                Text("Added \(pin.savedAt.formatted(.relative(presentation: .named)))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: 0)
        }
        .accessibilityElement(children: .combine)
    }
}
