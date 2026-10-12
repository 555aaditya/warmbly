import SwiftUI

/// `GET /me/danger-zone`: the account summary and any deletion in its grace window.
struct AccountDangerZone: Decodable, Sendable {
    var graceDays: Int?
    var pendingDeletion: PendingAccountDeletion?

    enum CodingKeys: String, CodingKey {
        case graceDays = "grace_days"
        case pendingDeletion = "pending_deletion"
    }
}

struct PendingAccountDeletion: Decodable, Sendable {
    var executeAfter: Date?
    var status: String?

    enum CodingKeys: String, CodingKey {
        case executeAfter = "execute_after"
        case status
    }
}

private struct ScheduleAccountDeletionBody: Encodable {
    var confirmation: String
}

/// Deleting the account from the phone: type the email, confirm it is you, and
/// the account is removed after the grace window unless the deletion is cancelled.
struct DeleteAccountSection: View {
    @Environment(AppEnvironment.self) private var env

    @State private var status: AccountDangerZone?
    @State private var expanded = false
    @State private var confirmation = ""
    @State private var busy = false
    @State private var showReauth = false
    @State private var errorMessage: String?
    @FocusState private var focused: Bool

    private var email: String { env.session.user?.email ?? "" }
    private var graceDays: Int { status?.graceDays ?? 30 }
    private var pending: PendingAccountDeletion? {
        guard let p = status?.pendingDeletion, p.status == nil || p.status == "pending" else { return nil }
        return p
    }

    private var confirmationMatches: Bool {
        !email.isEmpty && confirmation.trimmingCharacters(in: .whitespaces).lowercased() == email.lowercased()
    }

    var body: some View {
        Group {
            if let pending {
                pendingSection(pending)
            } else {
                requestSection
            }
        }
        .animation(.snappy, value: expanded)
        .animation(.snappy, value: pending != nil)
        .task { await load() }
        .sheet(isPresented: $showReauth) {
            ReauthSheet(
                reason: "Deleting your account needs a recent sign-in.",
                actionTitle: "Confirm and delete"
            ) {
                Task { await schedule() }
            }
        }
        .sensoryFeedback(.error, trigger: errorMessage)
    }

    // MARK: Sections

    private func pendingSection(_ pending: PendingAccountDeletion) -> some View {
        let when = pending.executeAfter.map { $0.formatted(.dateTime.month(.wide).day().year()) }
        return SettingsSection(
            "Delete account",
            footer: "Your account and every workspace you own are removed for good on that day. Cancel any time before then and nothing changes."
        ) {
            SettingsRow(
                icon: "trash.fill", color: WTheme.negative, title: "Scheduled for deletion",
                value: when, valueColor: WTheme.negative, accessory: .none
            )
            SettingsButtonRow(title: "Cancel deletion", busy: busy) {
                Task { await cancel() }
            }
            errorRow
        }
    }

    private var requestSection: some View {
        SettingsSection(
            "Delete account",
            footer: expanded
                ? "This deletes your account and every workspace you own. Type \(email) to confirm. Nothing is removed for \(graceDays) days, and you can cancel until then."
                : nil,
            dividerLeading: expanded ? 16 : 58
        ) {
            if !expanded {
                Button {
                    expanded = true
                    focused = true
                } label: {
                    SettingsRow(icon: "trash.fill", color: WTheme.negative, title: "Delete account", destructive: true)
                }
                .buttonStyle(MoreRowPressStyle())
            } else {
                SettingsFieldRow(label: "Your email") {
                    TextField(email, text: $confirmation)
                        .textContentType(.emailAddress)
                        .keyboardType(.emailAddress)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .focused($focused)
                        .submitLabel(.done)
                        .onSubmit { if confirmationMatches { Task { await schedule() } } }
                }
                errorRow
                HStack(spacing: 0) {
                    SettingsButtonRow(title: "Cancel", disabled: busy) {
                        confirmation = ""
                        errorMessage = nil
                        expanded = false
                    }
                    Rectangle().fill(WTheme.hairline).frame(width: 1, height: 28)
                    SettingsButtonRow(title: "Delete account", role: .destructive, busy: busy, disabled: !confirmationMatches) {
                        Task { await schedule() }
                    }
                }
            }
        }
    }

    @ViewBuilder
    private var errorRow: some View {
        if let errorMessage {
            Text(errorMessage)
                .font(.system(size: 13.5))
                .foregroundStyle(WTheme.negative)
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 16)
                .padding(.vertical, 12)
                .transition(.opacity)
        }
    }

    // MARK: Actions

    private func load() async {
        if let fresh: AccountDangerZone = try? await env.api.get("me/danger-zone") {
            status = fresh
        }
    }

    private func schedule() async {
        guard confirmationMatches, !busy else { return }
        busy = true
        defer { busy = false }
        errorMessage = nil
        do {
            let _: EmptyBody = try await env.api.post("me/danger-zone/delete", body: ScheduleAccountDeletionBody(confirmation: confirmation))
            confirmation = ""
            expanded = false
            await load()
        } catch let error as APIError where error.code == "reauth_required" {
            showReauth = true
        } catch {
            errorMessage = (error as? APIError)?.errorDescription ?? error.localizedDescription
        }
    }

    private func cancel() async {
        guard !busy else { return }
        busy = true
        defer { busy = false }
        errorMessage = nil
        do {
            let _: EmptyBody = try await env.api.delete("me/danger-zone/delete")
            await load()
        } catch {
            errorMessage = (error as? APIError)?.errorDescription ?? error.localizedDescription
        }
    }
}
