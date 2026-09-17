import SwiftUI

/// Optional two-step wizard shown after sign-up. Each step saves on its own,
/// so leaving midway keeps whatever was already answered.
struct OnboardingView: View {
    enum Step: Hashable { case pinterest }

    @Environment(AppModel.self) private var model
    @State private var path: [Step] = []

    private func finish() {
        model.isOnboardingPresented = false
    }

    var body: some View {
        NavigationStack(path: $path) {
            ReferralSourceForm(stepLabel: "Step 1 of 2") {
                path.append(.pinterest)
            }
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Skip") { path.append(.pinterest) }
                }
            }
            .navigationDestination(for: Step.self) { _ in
                PinterestHandleForm(stepLabel: "Step 2 of 2", actionTitle: "Import My Saves") {
                    finish()
                }
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button("Not Now") { finish() }
                    }
                }
            }
        }
    }
}

struct ReferralSourceForm: View {
    var stepLabel: String?
    var onSaved: () -> Void

    @Environment(AppModel.self) private var model
    @State private var saving: ReferralSource?
    @State private var errorMessage: String?

    var body: some View {
        List {
            Section {
                OnboardingHeader(
                    stepLabel: stepLabel,
                    title: "How did you find Fitkit?",
                    subtitle: "This helps us understand where people discover us."
                )
            }
            Section {
                ForEach(ReferralSource.allCases) { source in
                    Button {
                        save(source)
                    } label: {
                        HStack(spacing: 14) {
                            Image(systemName: source.symbolName)
                                .foregroundStyle(Color.accentColor)
                                .frame(width: 24)
                            Text(source.title)
                                .foregroundStyle(Color.primary)
                            Spacer()
                            if saving == source {
                                ProgressView()
                            } else if model.user?.referralSource == source.rawValue {
                                Image(systemName: "checkmark")
                                    .fontWeight(.semibold)
                                    .foregroundStyle(.tint)
                            }
                        }
                    }
                    .disabled(saving != nil)
                    .accessibilityIdentifier("referral.\(source.rawValue)")
                }
            } footer: {
                if let errorMessage {
                    Text(errorMessage).foregroundStyle(.red)
                }
            }
        }
        .navigationBarTitleDisplayMode(.inline)
    }

    private func save(_ source: ReferralSource) {
        saving = source
        errorMessage = nil
        Task {
            defer { saving = nil }
            do {
                model.update(try await model.api.setReferralSource(source))
                onSaved()
            } catch {
                if !model.handleUnauthorized(error) { errorMessage = error.localizedDescription }
            }
        }
    }
}

struct PinterestHandleForm: View {
    var stepLabel: String?
    var actionTitle = "Save"
    var onSaved: () -> Void

    @Environment(AppModel.self) private var model
    @State private var handle = ""
    @State private var isSaving = false
    @State private var errorMessage: String?
    @FocusState private var isFocused: Bool

    var body: some View {
        Form {
            Section {
                OnboardingHeader(
                    stepLabel: stepLabel,
                    title: "Connect Pinterest",
                    subtitle: "We'll bring in the pins you've saved publicly so you can shop them."
                )
            }
            Section {
                HStack {
                    Image(systemName: "at")
                        .foregroundStyle(.secondary)
                    TextField("Username or profile link", text: $handle)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .keyboardType(.URL)
                        .submitLabel(.done)
                        .focused($isFocused)
                        .onSubmit(save)
                        .accessibilityIdentifier("pinterest.handle")
                }
            } footer: {
                if let errorMessage {
                    Label(errorMessage, systemImage: "exclamationmark.circle.fill")
                        .foregroundStyle(.red)
                } else {
                    Text("For example, “janedoe” or pinterest.com/janedoe. Your profile and saves need to be public.")
                }
            }

            Section {
                Button(action: save) {
                    ZStack {
                        Text(actionTitle).opacity(isSaving ? 0 : 1)
                        if isSaving { ProgressView() }
                    }
                    .frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(handle.trimmingCharacters(in: .whitespaces).isEmpty || isSaving)
                .accessibilityIdentifier("pinterest.save")
            }
            .listRowBackground(Color.clear)
            .listRowInsets(EdgeInsets())
        }
        .navigationBarTitleDisplayMode(.inline)
        .onAppear {
            if handle.isEmpty { handle = model.user?.pinterestUsername ?? "" }
            isFocused = handle.isEmpty
        }
    }

    private func save() {
        guard !isSaving else { return }
        isSaving = true
        errorMessage = nil
        Task {
            defer { isSaving = false }
            do {
                let user = try await model.api.setPinterestHandle(handle)
                model.update(user)
                handle = user.pinterestUsername ?? handle
                onSaved()
            } catch {
                if !model.handleUnauthorized(error) { errorMessage = error.localizedDescription }
            }
        }
    }
}

private struct OnboardingHeader: View {
    var stepLabel: String?
    var title: String
    var subtitle: String

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            if let stepLabel {
                Text(stepLabel)
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(.tint)
            }
            Text(title)
                .font(.title2.bold())
            Text(subtitle)
                .font(.subheadline)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .listRowBackground(Color.clear)
        .listRowInsets(EdgeInsets(top: 8, leading: 4, bottom: 0, trailing: 4))
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isHeader)
    }
}
