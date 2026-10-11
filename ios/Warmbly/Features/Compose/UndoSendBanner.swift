import SwiftUI

/// Bottom-anchored undo-send capsule shown after an instant send: a live
/// countdown to `scheduledAt` (the backend queues instant sends a short
/// window into the future) plus an Undo button that cancels the queued task.
/// When the countdown hits zero it flips briefly to "Sent" and auto-dismisses
/// through `onExpired`. Styled after the unibox toast (ink capsule).
struct UndoSendBanner: View {
    let scheduledAt: Date
    /// Cancels the queued send; the caller owns dismissal and error handling.
    var onUndo: () async -> Void
    /// The window elapsed without an undo; the caller clears the banner state.
    var onExpired: () -> Void

    @State private var isCancelling = false
    @State private var sent = false

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: sent ? "checkmark.circle.fill" : "paperplane")
                .font(.system(size: 13, weight: .medium))
                .foregroundStyle(sent ? WTheme.positive : WTheme.inkInverse.opacity(0.7))
                .contentTransition(.symbolEffect(.replace))
            if sent {
                Text("Sent")
                    .font(.system(size: 13.5, weight: .medium))
                    .foregroundStyle(WTheme.inkInverse)
            } else {
                TimelineView(.periodic(from: .now, by: 0.5)) { timeline in
                    Text("Sending in \(remaining(at: timeline.date))s")
                        .font(WFont.meta(12.5, weight: .medium))
                        .monospacedDigit()
                        .foregroundStyle(WTheme.inkInverse)
                        .contentTransition(.numericText(countsDown: true))
                }
                Button {
                    guard !isCancelling else { return }
                    isCancelling = true
                    Task {
                        await onUndo()
                        isCancelling = false
                    }
                } label: {
                    Group {
                        if isCancelling {
                            ProgressView()
                                .controlSize(.small)
                                .tint(WTheme.ink)
                        } else {
                            Text("Undo")
                                .font(.system(size: 13, weight: .medium))
                                .foregroundStyle(WTheme.ink)
                        }
                    }
                    .padding(.horizontal, 12)
                    .frame(minWidth: 56, minHeight: 28)
                    .background(WTheme.inkInverse, in: Capsule())
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Undo send")
            }
        }
        .padding(.leading, 16)
        .padding(.trailing, sent ? 16 : 5)
        .frame(height: 38)
        .background(WTheme.ink, in: Capsule())
        .animation(.snappy, value: sent)
        .task(id: scheduledAt) {
            let delay = scheduledAt.timeIntervalSinceNow
            if delay > 0 {
                try? await Task.sleep(for: .seconds(delay))
            }
            guard !Task.isCancelled else { return }
            withAnimation(.snappy) { sent = true }
            try? await Task.sleep(for: .seconds(1.2))
            guard !Task.isCancelled else { return }
            onExpired()
        }
    }

    private func remaining(at date: Date) -> Int {
        max(0, Int(scheduledAt.timeIntervalSince(date).rounded(.up)))
    }
}
