import SwiftUI

struct OrdersView: View {
    @Environment(AppModel.self) private var app
    @State private var looks: [Look] = []
    @State private var isLoading = true
    @State private var errorMessage: String?

    var body: some View {
        Group {
            if isLoading {
                ProgressView()
            } else if looks.isEmpty {
                ContentUnavailableView {
                    Label("No Orders Yet", systemImage: "bag")
                } description: {
                    Text("Tap Get This Look on a pin to check out its pieces.")
                }
            } else {
                List {
                    Section {
                        ForEach(looks) { look in
                            NavigationLink {
                                LookOrderView(look: look, onChange: { update(with: $0) })
                            } label: {
                                LookRow(look: look, imageURL: app.api.resolve(look.pinImageUrl))
                            }
                        }
                    } footer: {
                        if let errorMessage {
                            Text(errorMessage).foregroundStyle(.red)
                        }
                    }
                }
                .accessibilityIdentifier("orders.list")
            }
        }
        .navigationTitle("Orders")
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
        .refreshable { await load() }
    }

    private func load() async {
        do {
            looks = try await app.api.looks()
        } catch {
            if !app.handleUnauthorized(error) { errorMessage = error.localizedDescription }
        }
        isLoading = false
    }

    private func update(with look: Look) {
        if let index = looks.firstIndex(where: { $0.id == look.id }) {
            looks[index] = look
        }
    }
}

private struct LookRow: View {
    let look: Look
    let imageURL: URL?

    var body: some View {
        HStack(spacing: 12) {
            PinImage(url: imageURL, dominantColor: "")
                .frame(width: 44, height: 56)
                .clipShape(.rect(cornerRadius: 8))
            VStack(alignment: .leading, spacing: 3) {
                Text(look.pinTitle.isEmpty ? "Saved pin" : look.pinTitle)
                    .font(.subheadline)
                    .lineLimit(2)
                Text(look.createdAt, format: .dateTime.day().month().year())
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Text(statusSummary)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: 0)
        }
        .accessibilityElement(children: .combine)
    }

    private var statusSummary: String {
        let relevant = look.stores.filter { $0.status != .skipped }
        let done = relevant.filter { [.ordered, .shipped, .delivered].contains($0.status) }.count
        return "\(done) of \(relevant.count) stores ordered"
    }
}
