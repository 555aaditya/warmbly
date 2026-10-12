import SwiftUI

/// Creating a workspace wears the same air as sign-up: the sky with its
/// flight ambience and a step badge, a full-bleed white sheet below. Two
/// steps: name the workspace, then optionally invite teammates. The org is
/// created (and invites sent) at the very end, so the flow works both as the
/// zero-orgs gate (the session flips to ready underneath it) and as a cover
/// from the workspace switcher.
struct WorkspaceCreateFlow: View {
    /// How the flow is presented: the `.selectOrg` gate offers Sign out and
    /// cannot be dismissed; a cover (switcher/picker) offers Cancel.
    enum Context {
        case gate
        case cover
    }

    private enum Page: Int, CaseIterable {
        case name, invite

        var icon: String {
            switch self {
            case .name: "briefcase.fill"
            case .invite: "person.badge.plus"
            }
        }

        var skyLabel: String {
            switch self {
            case .name: "Your workspace"
            case .invite: "Invite your team"
            }
        }
    }

    @Environment(AppEnvironment.self) private var env

    var context: Context = .cover
    /// Cover presenters pass their `isPresented` reset here; the environment
    /// DismissAction can no-op when the cover is launched from inside a
    /// detented sheet, so dismissal goes through the binding directly.
    var onClose: () -> Void = {}

    @State private var page = Page.name
    @State private var direction = 1.0
    @State private var name = ""
    @State private var inviteEmails: [InviteEntry] = [InviteEntry()]
    @State private var busy = false
    @State private var errorMessage: String?
    @State private var errorPulse = 0

    @FocusState private var nameFocused: Bool
    @FocusState private var focusedInvite: UUID?

    private struct InviteEntry: Identifiable {
        let id = UUID()
        var email = ""
    }

    var body: some View {
        ZStack(alignment: .bottom) {
            SkyBackdrop()
            VStack(spacing: 0) {
                topBar
                skyArea
                sheet
            }
        }
        .sensoryFeedback(.impact(weight: .light), trigger: page)
        .sensoryFeedback(.error, trigger: errorPulse)
    }

    private var pageTransition: AnyTransition {
        .asymmetric(
            insertion: .move(edge: direction > 0 ? .trailing : .leading).combined(with: .opacity),
            removal: .move(edge: direction > 0 ? .leading : .trailing).combined(with: .opacity)
        )
    }

    // MARK: - Sky chrome

    private var topBar: some View {
        FlowTopBar(
            onBack: page == .name ? nil : { goBack() },
            progress: (page.rawValue, Page.allCases.count)
        ) {
            if context == .gate {
                // Nothing to go back to without a workspace; the only exit
                // is signing out, like the web's select-org gate.
                FlowSkyPill(title: "Sign out") {
                    Task { await env.session.logout() }
                }
                .disabled(busy)
            } else {
                // Presented as a cover: the app's standard circular close.
                FlowSkyButton(symbol: "xmark", label: "Cancel") { onClose() }
                    .disabled(busy)
            }
        }
    }

    /// The sky between the top bar and the sheet: the page's icon and name
    /// when there is room, plain sky while the keyboard is up.
    private var skyArea: some View {
        FlowSkyStage(icon: page.icon, title: page.skyLabel) {
            nameFocused = false
            focusedInvite = nil
        }
        .frame(maxWidth: .infinity, minHeight: 88, maxHeight: .infinity)
    }

    private var sheet: some View {
        VStack(alignment: .leading, spacing: 0) {
            // Always a ScrollView: ViewThatFits swaps identities when the
            // keyboard shrinks the space, which destroys the focused field.
            ScrollView {
                pageContent
            }
            .scrollBounceBehavior(.basedOnSize)
            .scrollDismissesKeyboard(.interactively)

            footer
        }
        .padding(.horizontal, 24)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background {
            UnevenRoundedRectangle(cornerRadii: .init(topLeading: 36, topTrailing: 36))
                .fill(WTheme.canvas
                    .shadow(.drop(color: Color(hex: 0x0F172A).opacity(0.28), radius: 34, y: -6)))
                .overlay(
                    UnevenRoundedRectangle(cornerRadii: .init(topLeading: 36, topTrailing: 36))
                        .stroke(WTheme.sheetEdge, lineWidth: 1)
                )
                .padding(.bottom, -600)
                .ignoresSafeArea()
        }
        .geometryGroup()
        .layoutPriority(1)
    }

    private var pageContent: some View {
        Group {
            switch page {
            case .name: namePage
            case .invite: invitePage
            }
        }
        .padding(.top, 30)
        .padding(.bottom, 8)
        .transition(pageTransition)
    }

    private var footer: some View {
        VStack(spacing: 12) {
            if let errorMessage {
                Text(errorMessage)
                    .font(.system(size: 13.5))
                    .foregroundStyle(WTheme.negative)
                    .frame(maxWidth: .infinity)
                    .multilineTextAlignment(.center)
                    .transition(.opacity)
            }

            Button {
                Task { await advance() }
            } label: {
                Group {
                    if busy {
                        ProgressView().tint(WTheme.inkInverse)
                    } else {
                        HStack(spacing: 8) {
                            Text(page == .invite ? "Create workspace" : "Continue")
                            if page == .invite {
                                Image(systemName: "paperplane.fill")
                                    .font(.system(size: 14, weight: .semibold))
                            }
                        }
                    }
                }
                .font(.system(size: 16.5, weight: .medium))
                .foregroundStyle(WTheme.inkInverse)
                .frame(maxWidth: .infinity)
                .frame(height: 54)
                .background(WTheme.ink.opacity(!canAdvance && !busy ? 0.18 : 1), in: Capsule())
            }
            .buttonStyle(PressableButtonStyle())
            .disabled(busy || !canAdvance)
            .animation(.easeOut(duration: 0.18), value: canAdvance)

            if page == .invite {
                Button("Skip for now") {
                    inviteEmails = [InviteEntry()]
                    Task { await advance(skipping: true) }
                }
                .font(.system(size: 14, weight: .medium))
                .foregroundStyle(.secondary)
                .disabled(busy)
                .transition(.opacity)
            }
        }
        .padding(.top, 10)
        .padding(.bottom, 14)
        .animation(.easeOut(duration: 0.2), value: page)
        .animation(.easeOut(duration: 0.2), value: errorMessage != nil)
    }

    // MARK: - Pages

    private func pageTitle(_ title: String, _ subtitle: String) -> some View {
        VStack(alignment: .leading, spacing: 7) {
            Text(title)
                .displayFont(32)
                .foregroundStyle(WTheme.ink)
            Text(subtitle)
                .font(.system(size: 15.5))
                .foregroundStyle(WTheme.inkSecondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.bottom, 22)
    }

    private var namePage: some View {
        VStack(alignment: .leading, spacing: 0) {
            pageTitle(
                "Name your workspace",
                "One home for your team's mailboxes, contacts and campaigns."
            )

            TextField("Acme outbound", text: $name)
                .focused($nameFocused)
                .submitLabel(.continue)
                .onSubmit { Task { await advance() } }
                .font(.system(size: 16.5))
                .padding(.horizontal, 16)
                .frame(height: 56)
                .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 17))
                .overlay(
                    RoundedRectangle(cornerRadius: 17)
                        .strokeBorder(nameFocused ? WTheme.accent : .clear, lineWidth: 1.8)
                )
                .animation(.easeOut(duration: 0.18), value: nameFocused)

            Text("You'll be the owner. Rename it or invite more people anytime.")
                .font(.system(size: 13))
                .foregroundStyle(.tertiary)
                .padding(.top, 12)
        }
        .onAppear {
            if name.isEmpty { nameFocused = true }
        }
    }

    private var invitePage: some View {
        VStack(alignment: .leading, spacing: 0) {
            pageTitle(
                "Invite your team",
                "Optional. They'll get an email invitation to join \(name.trimmingCharacters(in: .whitespaces))."
            )

            VStack(spacing: 12) {
                ForEach($inviteEmails) { $entry in
                    inviteField($entry)
                }
            }

            if inviteEmails.count < 5 {
                Button {
                    withAnimation(.spring(response: 0.35, dampingFraction: 0.8)) {
                        inviteEmails.append(InviteEntry())
                    }
                } label: {
                    Label("Add another teammate", systemImage: "plus.circle.fill")
                        .font(.system(size: 14.5, weight: .semibold))
                        .foregroundStyle(WTheme.accent)
                }
                .buttonStyle(PressableButtonStyle())
                .padding(.top, 14)
            }

            Text("Everyone joins as a member. Fine-tune roles later in Team.")
                .font(.system(size: 13))
                .foregroundStyle(.tertiary)
                .padding(.top, 12)
        }
    }

    private func inviteField(_ entry: Binding<InviteEntry>) -> some View {
        HStack(spacing: 8) {
            Image(systemName: "envelope")
                .font(.system(size: 14, weight: .medium))
                .foregroundStyle(.tertiary)
            TextField("teammate@company.com", text: entry.email)
                .textContentType(.emailAddress)
                .keyboardType(.emailAddress)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .focused($focusedInvite, equals: entry.wrappedValue.id)
                .font(.system(size: 16))
            if inviteEmails.count > 1 {
                Button {
                    withAnimation(.spring(response: 0.35, dampingFraction: 0.8)) {
                        inviteEmails.removeAll { $0.id == entry.wrappedValue.id }
                    }
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.system(size: 16))
                        .foregroundStyle(.tertiary)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Remove teammate")
            }
        }
        .padding(.horizontal, 16)
        .frame(height: 54)
        .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 17))
        .overlay(
            RoundedRectangle(cornerRadius: 17)
                .strokeBorder(focusedInvite == entry.wrappedValue.id ? WTheme.accent : .clear, lineWidth: 1.8)
        )
        .animation(.easeOut(duration: 0.18), value: focusedInvite)
    }

    // MARK: - Flow

    private var trimmedName: String { name.trimmingCharacters(in: .whitespaces) }

    /// Loose shape check, mirroring the paste importer: enough to catch typos
    /// without rejecting unusual but valid addresses.
    private func looksLikeEmail(_ raw: String) -> Bool {
        let t = raw.trimmingCharacters(in: .whitespaces)
        return t.contains("@") && t.contains(".") && !t.hasSuffix("@")
    }

    private var validInvites: [String] {
        inviteEmails
            .map { $0.email.trimmingCharacters(in: .whitespaces).lowercased() }
            .filter { looksLikeEmail($0) }
    }

    private var canAdvance: Bool {
        switch page {
        case .name:
            return trimmedName.count >= 2
        case .invite:
            // Empty fields are fine (invites are optional), but a half-typed
            // address should be fixed or cleared, not silently dropped.
            return inviteEmails.allSatisfy {
                let t = $0.email.trimmingCharacters(in: .whitespaces)
                return t.isEmpty || looksLikeEmail(t)
            }
        }
    }

    private func goBack() {
        guard let previous = Page(rawValue: page.rawValue - 1) else { return }
        errorMessage = nil
        direction = -1
        withAnimation(.spring(response: 0.45, dampingFraction: 0.86)) {
            page = previous
        }
    }

    private func advance(skipping: Bool = false) async {
        guard canAdvance || skipping else { return }
        errorMessage = nil
        if page == .invite {
            await submit()
            return
        }
        nameFocused = false
        direction = 1
        withAnimation(.spring(response: 0.45, dampingFraction: 0.86)) {
            page = Page(rawValue: page.rawValue + 1) ?? .invite
        }
    }

    /// Creates the workspace (the session switches into it), then sends the
    /// invitations best-effort: the workspace exists either way, and failed
    /// invites reappear as absent rows in Team where they can be re-sent.
    private func submit() async {
        busy = true
        do {
            try await env.session.createOrganization(name: trimmedName)
            for email in validInvites {
                let _: MoreInviteResponse? = try? await env.api.post(
                    "organization/members/invite",
                    body: MoreInviteBody(email: email, roleIDs: [])
                )
            }
            UINotificationFeedbackGenerator().notificationOccurred(.success)
            if context == .cover { onClose() }
            // In the gate context the session phase flips to .ready and
            // RootView swaps this flow out on its own.
        } catch {
            withAnimation(.easeOut(duration: 0.2)) {
                errorMessage = (error as? APIError)?.errorDescription ?? error.localizedDescription
            }
            errorPulse += 1
        }
        busy = false
    }
}
