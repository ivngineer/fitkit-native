import SwiftUI

struct SettingsView: View {
    var onImport: () -> Void

    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var isConfirmingDelete = false
    @State private var isDeleting = false
    @State private var isConfirmingClear = false
    @State private var localDataSize: Int64?
    @State private var errorMessage: String?
    @State private var serverURL = ""

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    LabeledContent("Email", value: app.user?.email ?? "")
                } header: {
                    Text("Account")
                } footer: {
                    if app.isOffline {
                        Text("You're offline. Changes to your account need a connection to the Fitkit server.")
                    }
                }

                Section {
                    NavigationLink { OrdersView() } label: { Text("Orders") }
                        .disabled(app.isOffline)
                        .accessibilityIdentifier("settings.orders")
                    NavigationLink { AddressesView() } label: { Text("Addresses") }
                        .disabled(app.isOffline)
                        .accessibilityIdentifier("settings.addresses")
                    NavigationLink { SizesView() } label: { Text("Sizes") }
                        .disabled(app.isOffline)
                        .accessibilityIdentifier("settings.sizes")
                } header: {
                    Text("Shopping")
                } footer: {
                    Text("Some store links may earn Fitkit a commission. It never changes your price. You always pay the store directly.")
                }

                Section {
                    NavigationLink {
                        PinterestHandleForm { dismiss(); onImport() }
                    } label: {
                        LabeledContent("Username") {
                            Text(app.user?.pinterestUsername.map { "@\($0)" } ?? "Not connected")
                        }
                    }
                    .disabled(app.isOffline)
                    Button("Import Saves Now") {
                        dismiss()
                        onImport()
                    }
                    .disabled(app.isOffline || app.user?.pinterestUsername == nil)
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
                    .disabled(app.isOffline)
                }

                Section {
                    LabeledContent("Saved on This Device") {
                        if let localDataSize {
                            Text(localDataSize, format: .byteCount(style: .file))
                        } else {
                            ProgressView()
                        }
                    }
                    Button("Clear Local Data", role: .destructive) {
                        isConfirmingClear = true
                    }
                    .accessibilityIdentifier("settings.clearLocalData")
                } header: {
                    Text("Offline")
                } footer: {
                    Text("Your pins, their pieces and shopping links are kept on this device so you can browse without a connection. Clearing them frees up space and doesn't change anything in your account.")
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
                    .disabled(isDeleting || app.isOffline)
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
            .confirmationDialog("Clear local data?", isPresented: $isConfirmingClear, titleVisibility: .visible) {
                Button("Clear Local Data", role: .destructive, action: clearLocalData)
            } message: {
                Text("Pins, their pieces and shopping links will be removed from this device. Everything stays in your Fitkit account and downloads again when you're online.")
            }
            .onAppear { serverURL = app.baseURL.absoluteString }
            .task(id: app.localDataResetCount) { await measureLocalData() }
        }
    }

    private func clearLocalData() {
        app.clearLocalData()
    }

    private func measureLocalData() async {
        let root = app.store.root
        localDataSize = await Task.detached { LocalStore.size(of: root) }.value
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
