import SwiftUI

struct SettingsView: View {
    var onImport: () -> Void

    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var isConfirmingDelete = false
    @State private var isDeleting = false
    @State private var errorMessage: String?
    @State private var serverURL = ""

    var body: some View {
        NavigationStack {
            Form {
                Section("Account") {
                    LabeledContent("Email", value: app.user?.email ?? "")
                }

                Section {
                    NavigationLink {
                        PinterestHandleForm { dismiss(); onImport() }
                    } label: {
                        LabeledContent("Username") {
                            Text(app.user?.pinterestUsername.map { "@\($0)" } ?? "Not connected")
                        }
                    }
                    Button("Import Saves Now") {
                        dismiss()
                        onImport()
                    }
                    .disabled(app.user?.pinterestUsername == nil)
                } header: {
                    Text("Pinterest")
                } footer: {
                    Text("Fitkit imports pins from your public Pinterest saves.")
                }

                Section {
                    NavigationLink {
                        ReferralSourceForm {}
                    } label: {
                        LabeledContent("How You Found Fitkit") {
                            Text(app.user?.referralSource.flatMap(ReferralSource.init(rawValue:))?.title ?? "Not set")
                        }
                    }
                }

                #if DEBUG
                Section {
                    TextField("Server URL", text: $serverURL)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .onSubmit(applyServerURL)
                } header: {
                    Text("Developer")
                } footer: {
                    Text("Changing the server signs you out.")
                }
                #endif

                Section {
                    Button("Sign Out") {
                        Task { await app.signOut() }
                    }
                    Button("Delete Account", role: .destructive) {
                        isConfirmingDelete = true
                    }
                    .disabled(isDeleting)
                } footer: {
                    if let errorMessage {
                        Text(errorMessage).foregroundStyle(.red)
                    }
                }
            }
            .navigationTitle("Account")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                }
            }
            .confirmationDialog("Delete your Fitkit account?", isPresented: $isConfirmingDelete, titleVisibility: .visible) {
                Button("Delete Account", role: .destructive, action: deleteAccount)
            } message: {
                Text("Your account and saved pins will be permanently removed. This can't be undone.")
            }
            .onAppear { serverURL = app.baseURL.absoluteString }
        }
    }

    private func deleteAccount() {
        isDeleting = true
        Task {
            defer { isDeleting = false }
            do {
                try await app.deleteAccount()
            } catch {
                if !app.handleUnauthorized(error) { errorMessage = error.localizedDescription }
            }
        }
    }

    private func applyServerURL() {
        guard let url = URL(string: serverURL.trimmingCharacters(in: .whitespaces)), url.scheme != nil,
              url != app.baseURL else { return }
        Task {
            await app.signOut()
            app.baseURL = url
        }
    }
}
