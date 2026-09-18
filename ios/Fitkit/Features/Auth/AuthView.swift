import SwiftUI

struct AuthView: View {
    enum Mode: String, CaseIterable, Identifiable {
        case signIn = "Sign In"
        case signUp = "Create Account"
        var id: Self { self }
    }

    enum Field: Hashable { case email, password }

    @Environment(AppModel.self) private var model
    @State private var mode: Mode = .signUp
    @State private var email = ""
    @State private var password = ""
    @State private var errorMessage: String?
    @State private var isSubmitting = false
    @FocusState private var focus: Field?

    /// UI tests pass -uiTesting because the strong-password AutoFill overlay swallows typed text.
    private var passwordContentType: UITextContentType? {
        if ProcessInfo.processInfo.arguments.contains("-uiTesting") { return nil }
        return mode == .signUp ? .newPassword : .password
    }

    private var canSubmit: Bool {
        email.contains("@") && password.count >= 8 && !isSubmitting
    }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    header
                }
                .listRowBackground(Color.clear)
                .listRowInsets(EdgeInsets())

                Section {
                    Picker("Mode", selection: $mode) {
                        ForEach(Mode.allCases) { Text($0.rawValue).tag($0) }
                    }
                    .pickerStyle(.segmented)
                    .labelsHidden()
                    .controlSize(.small)
                    .frame(maxWidth: 280)
                    .frame(maxWidth: .infinity)
                }
                .listRowBackground(Color.clear)
                .listRowInsets(EdgeInsets(top: 0, leading: 0, bottom: 8, trailing: 0))

                Section {
                    TextField("Email", text: $email)
                        .textContentType(ProcessInfo.processInfo.arguments.contains("-uiTesting") ? nil : .username)
                        .keyboardType(.emailAddress)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .submitLabel(.next)
                        .focused($focus, equals: .email)
                        .onSubmit { focus = .password }
                        .accessibilityIdentifier("auth.email")
                    SecureField("Password", text: $password)
                        .textContentType(passwordContentType)
                        .submitLabel(.go)
                        .focused($focus, equals: .password)
                        .onSubmit { if canSubmit { submit() } }
                        .accessibilityIdentifier("auth.password")
                } footer: {
                    if let errorMessage {
                        Label(errorMessage, systemImage: "exclamationmark.circle.fill")
                            .foregroundStyle(.red)
                    } else if mode == .signUp {
                        Text("Use at least 8 characters.")
                    }
                }

                Section {
                    Button(action: submit) {
                        ZStack {
                            Text(mode.rawValue).opacity(isSubmitting ? 0 : 1)
                            if isSubmitting { ProgressView() }
                        }
                        .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
                    .disabled(!canSubmit)
                    .accessibilityIdentifier("auth.submit")
                }
                .listRowBackground(Color.clear)
                .listRowInsets(EdgeInsets())
            }
            .scrollDismissesKeyboard(.interactively)
            .readableContentWidth()
            .onChange(of: mode) { errorMessage = nil }
        }
    }

    private var header: some View {
        VStack(spacing: 8) {
            Text("Fitkit")
                .font(.largeTitle.bold())
            Text("Turn the pins you love into a shopping list.")
                .font(.body)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 24)
        .padding(.bottom, 4)
    }

    private func submit() {
        focus = nil
        errorMessage = nil
        isSubmitting = true
        Task {
            defer { isSubmitting = false }
            do {
                switch mode {
                case .signIn: try await model.signIn(email: email, password: password)
                case .signUp: try await model.signUp(email: email, password: password)
                }
            } catch {
                errorMessage = error.localizedDescription
            }
        }
    }
}

#Preview {
    AuthView().environment(AppModel())
}
