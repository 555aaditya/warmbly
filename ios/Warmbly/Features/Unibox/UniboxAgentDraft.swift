import SwiftUI

// The inbox agent's suggested reply for a thread (`GET /v1/unibox/agent-drafts`),
// waiting on a person. Nothing goes out until someone sends or discards it.

struct AgentThreadDraft: Decodable, Identifiable, Sendable, Equatable {
    var id: String
    var threadID: String
    var toAddr: String
    var subject: String
    var body: String
    var intentClass: String?
    var confidence: Double?
    var status: String

    enum CodingKeys: String, CodingKey {
        case id, subject, body, confidence, status
        case threadID = "thread_id"
        case toAddr = "to_addr"
        case intentClass = "intent_class"
    }

    /// How the reply classifier read the message this answers.
    var intent: (label: String, tone: Tone)? {
        switch intentClass {
        case "positive": ("Interested", .emerald)
        case "negative": ("Not interested", .rose)
        case "neutral": ("Neutral", .slate)
        case "out_of_office": ("Out of office", .amber)
        case "auto_reply": ("Auto-reply", .slate)
        case "unsubscribe": ("Unsubscribe", .rose)
        default: nil
        }
    }
}

/// `POST /v1/unibox/agent-drafts/:id/approve`; an empty body sends the draft as written.
struct AgentDraftApproveBody: Encodable {
    var body: String?
}

/// The suggested reply at the foot of a thread: who it goes to, how the lead
/// read, a preview of the text, then Send, Edit and Discard. Editing happens in
/// the full reply composer, with its AI tools, not in the card.
struct AgentDraftCard: View {
    let draft: AgentThreadDraft
    let text: String
    let busy: Bool
    let onSend: () -> Void
    let onEdit: () -> Void
    let onRevert: () -> Void
    let onDiscard: () -> Void

    @State private var expanded = false

    private var edited: Bool { text != draft.body }
    /// Long drafts preview their opening and unfold on demand.
    private var isLong: Bool { text.count > 420 || text.filter(\.isNewline).count > 9 }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 8) {
                RemieSpark(size: 15, state: busy ? .thinking : .idle)
                Text(edited ? "Your edited reply" : "Drafted reply")
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundStyle(WTheme.ink)
                    .contentTransition(.opacity)
                if let intent = draft.intent {
                    StatusPill(text: intent.label.lowercased(), tone: intent.tone)
                }
                Spacer(minLength: 4)
                if edited {
                    Button("Revert", action: onRevert)
                        .font(.system(size: 12.5, weight: .medium))
                        .foregroundStyle(WTheme.inkSecondary)
                        .disabled(busy)
                        .transition(.opacity)
                }
            }

            Text("To \(draft.toAddr)")
                .font(.system(size: 12.5))
                .foregroundStyle(WTheme.inkSecondary)
                .lineLimit(1)
                .truncationMode(.middle)

            Button(action: onEdit) {
                VStack(alignment: .leading, spacing: 8) {
                    Text(text)
                        .font(.system(size: 15))
                        .foregroundStyle(WTheme.ink)
                        .lineSpacing(3)
                        .multilineTextAlignment(.leading)
                        .lineLimit(isLong && !expanded ? 8 : nil)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    HStack(spacing: 5) {
                        Image(systemName: "pencil")
                            .font(.system(size: 10.5, weight: .semibold))
                        Text("Tap to edit")
                            .font(.system(size: 12, weight: .medium))
                    }
                    .foregroundStyle(WTheme.inkTertiary)
                }
                .padding(14)
                .background(WTheme.canvas, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
                .overlay(
                    RoundedRectangle(cornerRadius: 14, style: .continuous)
                        .strokeBorder(WTheme.hairline, lineWidth: 1)
                )
                .contentShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
            }
            .buttonStyle(TapScaleStyle())
            .disabled(busy)
            .accessibilityHint("Opens the reply editor")

            if isLong {
                Button(expanded ? "Show less" : "Show the whole reply") {
                    withAnimation(.snappy) { expanded.toggle() }
                }
                .font(.system(size: 13, weight: .medium))
                .foregroundStyle(WTheme.accent)
            }

            HStack(spacing: 8) {
                Button(action: onSend) {
                    HStack(spacing: 6) {
                        if busy {
                            ProgressView().controlSize(.small).tint(WTheme.inkInverse)
                        } else {
                            Image(systemName: "arrow.up")
                                .font(.system(size: 12, weight: .semibold))
                        }
                        Text("Send reply")
                    }
                }
                .buttonStyle(InkButtonStyle(size: .small))
                .disabled(busy || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)

                Button(action: onEdit) {
                    HStack(spacing: 6) {
                        Image(systemName: "square.and.pencil")
                            .font(.system(size: 12, weight: .medium))
                        Text("Edit")
                    }
                }
                .buttonStyle(OutlineButtonStyle(size: .small))
                .disabled(busy)

                Spacer(minLength: 0)

                Button(action: onDiscard) {
                    Image(systemName: "trash")
                        .font(.system(size: 13, weight: .medium))
                        .foregroundStyle(WTheme.inkSecondary)
                        .frame(width: 34, height: 34)
                        .background(WTheme.raised, in: Circle())
                        .overlay(Circle().strokeBorder(WTheme.hairlineStrong, lineWidth: 1))
                }
                .buttonStyle(TapScaleStyle())
                .disabled(busy)
                .accessibilityLabel("Discard draft")
            }

            Text("Nothing is sent until you tap Send.")
                .font(.system(size: 12))
                .foregroundStyle(WTheme.inkTertiary)
        }
        .padding(14)
        .background(
            LinearGradient(
                colors: [Remie.sky500.opacity(0.07), WTheme.raised],
                startPoint: .top,
                endPoint: .bottom
            ),
            in: RoundedRectangle(cornerRadius: 18, style: .continuous)
        )
        .overlay(
            RoundedRectangle(cornerRadius: 18, style: .continuous)
                .strokeBorder(Remie.sky500.opacity(0.22), lineWidth: 1)
        )
        .animation(.snappy, value: edited)
    }
}
