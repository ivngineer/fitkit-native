import SwiftUI

struct SizesView: View {
    @Environment(AppModel.self) private var app
    @State private var values: [SizeCategory: String] = [:]
    @State private var isLoading = true
    @State private var isSaving = false
    @State private var errorMessage: String?

    var body: some View {
        Form {
            Section {
                if isLoading {
                    ProgressView()
                } else {
                    ForEach(SizeCategory.allCases) { category in
                        TextField(placeholder(for: category), text: binding(for: category))
                            .accessibilityIdentifier("sizes.\(category.rawValue)")
                    }
                }
            } header: {
                Text("Sizes")
            } footer: {
                if let errorMessage {
                    Text(errorMessage).foregroundStyle(.red)
                } else {
                    Text("Fitkit picks these sizes for you when you get a look.")
                }
            }
        }
        .navigationTitle("Sizes")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                if isSaving {
                    ProgressView()
                } else {
                    Button("Save", action: save)
                        .accessibilityIdentifier("sizes.save")
                }
            }
        }
        .task { await load() }
    }

    private func placeholder(for category: SizeCategory) -> String {
        switch category {
        case .top: "e.g. M"
        case .bottom: "e.g. 32"
        case .dress: "e.g. 8"
        case .outerwear: "e.g. L"
        case .shoes: "e.g. EU 39"
        }
    }

    private func binding(for category: SizeCategory) -> Binding<String> {
        Binding(
            get: { values[category] ?? "" },
            set: { values[category] = $0 }
        )
    }

    private func load() async {
        do {
            let saved = try await app.api.sizes()
            for (key, value) in saved {
                if let category = SizeCategory(rawValue: key) { values[category] = value }
            }
        } catch {
            if !app.handleUnauthorized(error) { errorMessage = error.localizedDescription }
        }
        isLoading = false
    }

    private func save() {
        isSaving = true
        Task {
            defer { isSaving = false }
            var payload: [String: String] = [:]
            for (category, value) in values {
                let trimmed = value.trimmingCharacters(in: .whitespaces)
                if !trimmed.isEmpty { payload[category.rawValue] = trimmed }
            }
            do {
                let saved = try await app.api.setSizes(payload)
                for (key, value) in saved {
                    if let category = SizeCategory(rawValue: key) { values[category] = value }
                }
                errorMessage = nil
            } catch {
                if !app.handleUnauthorized(error) { errorMessage = error.localizedDescription }
            }
        }
    }
}
