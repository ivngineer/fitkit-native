import SwiftUI

struct ImportStatusCard: View {
    let job: ImportJob
    var onRetry: () -> Void
    var onDismiss: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            icon
                .font(.title3)
                .frame(width: 28)

            VStack(alignment: .leading, spacing: 6) {
                Text(title)
                    .font(.subheadline.weight(.semibold))
                Text(detail)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)

                if job.isActive {
                    if let progress = job.progress {
                        ProgressView(value: progress)
                    } else {
                        ProgressView().progressViewStyle(.linear)
                    }
                } else if job.error?.retryable == true {
                    Button("Try Again", action: onRetry)
                        .buttonStyle(.bordered)
                        .controlSize(.small)
                        .padding(.top, 2)
                }
            }

            if !job.isActive {
                Button(action: onDismiss) {
                    Image(systemName: "xmark")
                        .font(.footnote.weight(.semibold))
                        .foregroundStyle(.secondary)
                        .padding(6)
                        .background(.quaternary, in: .circle)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Dismiss")
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(14)
        .background(.regularMaterial, in: .rect(cornerRadius: 18))
        .accessibilityElement(children: .contain)
    }

    @ViewBuilder private var icon: some View {
        switch job.status {
        case .queued, .running:
            Image(systemName: "arrow.down.circle.fill").foregroundStyle(.tint)
                .symbolEffect(.pulse, options: .repeating)
        case .done:
            Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
        case .failed:
            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
        }
    }

    private var title: String {
        switch job.status {
        case .queued: "Getting ready to import"
        case .running: job.discovered == 0 ? "Finding your saves on Pinterest" : "Importing your saves"
        case .done: "Import complete"
        case .failed: "Import didn't finish"
        }
    }

    private var detail: String {
        switch job.status {
        case .queued:
            return "@\(job.pinterestUsername)"
        case .running:
            if job.discovered == 0 { return "Looking through @\(job.pinterestUsername)’s public pins…" }
            return "\(job.processed) of \(job.discovered) pins"
        case .done:
            var parts = ["\(job.imported + job.reused) pins added"]
            if job.failed > 0 { parts.append("\(job.failed) couldn't be downloaded") }
            return parts.joined(separator: " · ")
        case .failed:
            return job.error?.message ?? "Something went wrong."
        }
    }
}
