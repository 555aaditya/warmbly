import SwiftUI

/// Autosaved compose drafts (`GET /v1/unibox/drafts`): tap to resume a draft
/// in the composer, swipe to delete. Pushed from the unibox drawer.
struct ComposeDraftsView: View {
    @Environment(AppEnvironment.self) private var env

    /// Opens the tapped draft in the compose sheet (owned by the unibox root).
    var onOpen: (ComposeDraft) -> Void

    @State private var store = ComposeDraftsStore()

    var body: some View {
        Group {
            if store.isLoading, !store.hasLoaded {
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if let error = store.errorMessage {
                EmptyStateView(title: "Couldn't load drafts", message: error)
            } else if store.drafts.isEmpty {
                EmptyStateView(
                    title: "No drafts",
                    message: "Emails you start composing are saved here automatically."
                )
            } else {
                List {
                    ForEach(store.drafts) { draft in
                        Button {
                            onOpen(draft)
                        } label: {
                            row(draft)
                        }
                        .buttonStyle(.plain)
                        .listRowBackground(WTheme.canvas)
                        .listRowSeparatorTint(WTheme.hairline)
                        .swipeActions(edge: .trailing) {
                            Button(role: .destructive) {
                                Task { await store.delete(env.api, id: draft.id) }
                            } label: {
                                Label("Delete", systemImage: "trash")
                            }
                        }
                    }
                }
                .listStyle(.plain)
                .scrollContentBackground(.hidden)
            }
        }
        .background(WTheme.canvas)
        .navigationTitle("Drafts")
        .navigationBarTitleDisplayMode(.inline)
        .toolbarBackground(WTheme.canvas, for: .navigationBar)
        .task { await store.load(env.api) }
        .refreshable { await store.load(env.api) }
    }

    private func row(_ draft: ComposeDraft) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 8) {
                Text(recipientsLine(draft))
                    .font(.system(size: 15.5, weight: .medium))
                    .foregroundStyle(WTheme.ink)
                    .lineLimit(1)
                Spacer(minLength: 8)
                Text(UniboxFormat.listTime(draft.updatedAt))
                    .font(WFont.meta(11))
                    .monospacedDigit()
                    .foregroundStyle(WTheme.inkTertiary)
            }
            if let subject = draft.subject, !subject.isEmpty {
                Text(subject)
                    .font(.system(size: 14))
                    .foregroundStyle(WTheme.inkSecondary)
                    .lineLimit(1)
            }
            if let snippet = draft.body?.trimmingCharacters(in: .whitespacesAndNewlines), !snippet.isEmpty {
                Text(snippet)
                    .font(.system(size: 13.5))
                    .foregroundStyle(WTheme.inkTertiary)
                    .lineLimit(2)
            }
        }
        .padding(.vertical, 6)
        .frame(maxWidth: .infinity, alignment: .leading)
        .contentShape(Rectangle())
    }

    private func recipientsLine(_ draft: ComposeDraft) -> String {
        let recipients = (draft.to ?? []).map(UniboxAddress.display)
        guard !recipients.isEmpty else { return "No recipient" }
        return recipients.joined(separator: ", ")
    }
}
