import SwiftUI
import UniformTypeIdentifiers

/// Full-screen "add a mailbox" flow with every source the dashboard offers
/// except per-mailbox sign-in: an app password, an admin grant over a whole
/// Workspace domain or Microsoft 365 organization, an inbox vendor's API, a
/// file or pasted list, and mailboxes already on a linked Warmbly Cloud
/// workspace. Every step shares one header; the option you tap carries its
/// mark into the next step. Dismissal always goes through `onClose()`.
struct MailboxConnectFlow: View {
    var onClose: () -> Void = {}
    var onConnected: ((EmailAccount) -> Void)? = nil
    /// A background import started; its mailboxes arrive as it runs.
    var onImported: (() -> Void)? = nil

    private enum Page: Hashable {
        case provider, google
        case twoStep, createPassword, appConnect
        case account, imap, smtp
        case grants, users
        case vendors, vendorKey, vendorMailboxes
        case fileSource, fileReview
        case running, done
    }

    private enum Route { case undecided, appPassword, grant, other, vendor, file, cloud }

    private enum WarmupOffer: Equatable {
        case offer, starting, active, failed(String)
    }

    private enum Field: Hashable {
        case name, email, appPassword, search, sharedPassword, paste
        case imapHost, imapPort, imapUser, imapPass
        case smtpHost, smtpPort, smtpUser, smtpPass
        case vendorLabel, vendorField(String)
    }

    @Environment(AppEnvironment.self) private var env
    @Environment(\.openURL) private var openURL
    @Namespace private var hero

    @State private var page = Page.provider
    @State private var route = Route.undecided
    @State private var chosen: String?
    @State private var direction = 1.0
    @State private var scroll = ScrollPosition(edge: .top)

    // What this instance offers.
    @State private var sourcesLoaded = false
    @State private var grantConfig: MailboxGrantConfig?
    @State private var grants: [MailboxGrant]?
    @State private var catalog: [VendorCatalogEntry]?
    @State private var vendorConnections: [VendorConnection] = []
    @State private var cloudMailboxes: [CloudWorkspaceMailbox] = []
    @State private var adopting: String?
    @State private var checkingGrant: String?

    // Picking mailboxes from a grant or a vendor account.
    @State private var grantProvider = GrantProvider.google
    @State private var grant: MailboxGrant?
    @State private var users: [GrantDirectoryUser]?
    @State private var vendorEntry: VendorCatalogEntry?
    @State private var vendorConnection: VendorConnection?
    @State private var vendorViaKey = false
    @State private var vendorMailboxes: [VendorMailbox]?
    @State private var listError: String?
    @State private var picked: Set<String> = []
    @State private var query = ""
    @State private var warmupImported = true
    @State private var showReauth = false

    // Vendor key form
    @State private var vendorLabel = ""
    @State private var vendorValues: [String: String] = [:]

    // File import
    @State private var fileFromGoogle = false
    @State private var pickingFile = false
    @State private var file: APIClient.UploadFile?
    @State private var pasted = ""
    @State private var preview: MailboxImportPreview?
    @State private var sharedPassword = ""

    // The background job a grant, vendor or file import starts.
    @State private var job: MailboxImportJob?

    // One mailbox
    @State private var name = ""
    @State private var email = ""
    @State private var appPassword = ""
    @State private var imapHost = ""
    @State private var imapPort = "993"
    @State private var imapUsername = ""
    @State private var imapPassword = ""
    @State private var smtpHost = ""
    @State private var smtpPort = "587"
    @State private var sameLogin = true
    @State private var smtpUsername = ""
    @State private var smtpPassword = ""
    @State private var filledFor: String?

    @State private var busy = false
    @State private var errorMessage: String?
    @State private var errorPulse = 0
    @State private var connectedAccount: EmailAccount?
    @State private var warmupOffer = WarmupOffer.offer

    @FocusState private var focused: Field?

    private static let push = Animation.spring(response: 0.5, dampingFraction: 0.86)

    var body: some View {
        ZStack(alignment: .top) {
            WTheme.canvas
                .ignoresSafeArea()
                .contentShape(Rectangle())
                .onTapGesture { focused = nil }
            sheet
        }
        .task {
            #if DEBUG
            if applyDebugLaunch() { return }
            #endif
            await loadSources()
        }
        .task(id: job?.id) { await pollJob() }
        .task(id: sharedPassword) { await refreshPreviewForSharedPassword() }
        .sheet(isPresented: $showReauth) {
            ReauthSheet { Task { await connectPicked() } }
        }
        .fileImporter(isPresented: $pickingFile, allowedContentTypes: Self.importTypes) { result in
            readFile(result)
        }
        .sensoryFeedback(.impact(weight: .light), trigger: page)
        .sensoryFeedback(.selection, trigger: chosen) { _, new in new != nil }
        .sensoryFeedback(.selection, trigger: picked)
        .sensoryFeedback(.error, trigger: errorPulse)
    }

    // MARK: - Paths

    private var pagePath: [Page] {
        switch route {
        case .undecided: return page == .google ? [.provider, .google] : [.provider]
        case .appPassword: return [.provider, .google, .twoStep, .createPassword, .appConnect, .done]
        case .grant: return (grantProvider == .google ? [.provider, .google] : [.provider]) + [.grants, .users, .running]
        case .other: return [.provider, .account, .imap, .smtp, .done]
        case .vendor: return [.provider, .vendors] + (vendorViaKey ? [.vendorKey] : []) + [.vendorMailboxes, .running]
        case .file: return (fileFromGoogle ? [.provider, .google] : [.provider]) + [.fileSource, .fileReview, .running]
        case .cloud: return [.provider, .done]
        }
    }

    private var stepIndex: Int { pagePath.firstIndex(of: page) ?? 0 }

    /// How far through the flow this step is; an estimate until a route is picked.
    private var progress: Double {
        if route == .undecided { return page == .provider ? 0.12 : 0.3 }
        return Double(stepIndex + 1) / Double(max(pagePath.count, 1))
    }

    private var finishedFlow: Bool { page == .done || (page == .running && job?.isRunning == false) }

    private var canGoBack: Bool { stepIndex > 0 && page != .done && page != .running }

    // MARK: - Chrome

    private var topBar: some View {
        HStack(spacing: 12) {
            ZStack {
                if canGoBack {
                    Button(action: goBack) { chromeCircle("chevron.left") }
                        .buttonStyle(TapScaleStyle())
                        .accessibilityLabel("Back")
                        .disabled(busy)
                        .transition(.opacity.combined(with: .scale(scale: 0.6)))
                }
            }
            .frame(width: 40, height: 40)

            Spacer(minLength: 8)
            GeometryReader { proxy in
                ZStack(alignment: .leading) {
                    Capsule().fill(WTheme.hairlineStrong)
                    Capsule()
                        .fill(finishedFlow ? WTheme.positive : WTheme.ink)
                        .frame(width: max(6, proxy.size.width * progress))
                }
            }
            .frame(width: 112, height: 5)
            .accessibilityElement()
            .accessibilityLabel("Step \(stepIndex + 1) of \(pagePath.count)")
            Spacer(minLength: 8)

            Button(action: onClose) { chromeCircle("xmark") }
                .buttonStyle(TapScaleStyle())
                .accessibilityLabel("Close")
                .disabled(busy)
        }
        .padding(.horizontal, 16)
        .padding(.top, 6)
        .padding(.bottom, 4)
        .animation(.spring(response: 0.55, dampingFraction: 0.8), value: progress)
        .animation(.spring(response: 0.4, dampingFraction: 0.8), value: canGoBack)
        .animation(.easeOut(duration: 0.3), value: finishedFlow)
    }

    private func chromeCircle(_ symbol: String) -> some View {
        Image(systemName: symbol)
            .font(.system(size: 14, weight: .medium))
            .foregroundStyle(WTheme.ink)
            .frame(width: 40, height: 40)
            .background(WTheme.raised, in: Circle())
            .overlay(Circle().strokeBorder(WTheme.hairlineStrong, lineWidth: 1))
    }

    // Full width and running under both bars, so a step scrolls from any edge
    // and fades beneath the chrome instead of being cut at it.
    private var sheet: some View {
        // Always a ScrollView: ViewThatFits swaps identities when the
        // keyboard shrinks the space, which destroys the focused field.
        ScrollView {
            pageContent
                .padding(.horizontal, 24)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .scrollPosition($scroll)
        .scrollBounceBehavior(.basedOnSize)
        .scrollDismissesKeyboard(.interactively)
        .scrollEdgeEffectStyle(.soft, for: .vertical)
        .safeAreaBar(edge: .top) { topBar }
        .safeAreaBar(edge: .bottom) { footer.padding(.horizontal, 24) }
        .geometryGroup()
    }

    private var pageContent: some View {
        Group {
            switch page {
            case .provider: providerPage
            case .google: googlePage
            case .twoStep: twoStepPage
            case .createPassword: createPasswordPage
            case .appConnect: appConnectPage
            case .account: accountPage
            case .imap: imapPage
            case .smtp: smtpPage
            case .grants: grantsPage
            case .users: usersPage
            case .vendors: vendorsPage
            case .vendorKey: vendorKeyPage
            case .vendorMailboxes: vendorMailboxesPage
            case .fileSource: fileSourcePage
            case .fileReview: fileReviewPage
            case .running: runningPage
            case .done: donePage
            }
        }
        .padding(.top, 22)
        .padding(.bottom, 12)
        .transition(.asymmetric(
            insertion: .move(edge: direction > 0 ? .trailing : .leading).combined(with: .opacity),
            removal: .move(edge: direction > 0 ? .leading : .trailing)
                .combined(with: .opacity)
                .combined(with: .scale(scale: 0.96))
        ))
    }

    // MARK: - Header and marks

    /// The one header every step uses: its mark, a title and a line under it.
    private func header(_ mark: String, _ title: String, _ subtitle: String) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            markView(mark, size: 56)
                .matchedGeometryEffect(id: mark, in: hero)
                .padding(.bottom, 18)
            Text(title)
                .displayFont(30)
                .foregroundStyle(WTheme.ink)
                .fixedSize(horizontal: false, vertical: true)
                .contentTransition(.opacity)
            Text(subtitle)
                .font(.system(size: 15.5))
                .foregroundStyle(WTheme.inkSecondary)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 7)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.bottom, 24)
    }

    /// The tile that stands for a source, at any size. The same id in a row and
    /// in the next step's header is what makes it fly between them.
    @ViewBuilder
    private func markView(_ mark: String, size: CGFloat) -> some View {
        switch mark {
        case "home": IconTile(symbol: "tray.and.arrow.down.fill", tone: .sky, size: size)
        case "google": frameTile(size) { ProviderMark(mark: .google, size: size * 0.5) }
        case "microsoft": frameTile(size) { ProviderMark(mark: .microsoft, size: size * 0.5) }
        case "other": frameTile(size) { logoGrid(["Host-zoho", "Host-yahoo", "Host-icloud", "Host-fastmail"], size: size) }
        case "vendor": frameTile(size) { logoGrid(["Vendor-inboxkit", "Vendor-zapmail", "Vendor-mailforge", "Vendor-scaledmail"], size: size) }
        case "grant": IconTile(symbol: "building.2.fill", tone: .sky, size: size)
        case "apppassword": IconTile(symbol: "key.fill", tone: .amber, size: size)
        case "file": IconTile(symbol: "tablecells.fill", tone: .emerald, size: size)
        case "done": IconTile(symbol: "checkmark", tone: .emerald, size: size)
        default:
            if mark.hasPrefix("vendor-") {
                let id = String(mark.dropFirst("vendor-".count))
                frameTile(size) {
                    ProviderMark(mark: MailboxBrand(provider: nil, mailHost: nil, vendor: id).mark, size: size * 0.56)
                }
            } else {
                IconTile(symbol: "envelope.fill", tone: .sky, size: size)
            }
        }
    }

    private func frameTile(_ size: CGFloat, @ViewBuilder content: () -> some View) -> some View {
        content()
            .frame(width: size, height: size)
            .background(.white, in: RoundedRectangle(cornerRadius: size * 0.3, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: size * 0.3, style: .continuous)
                    .strokeBorder(WTheme.hairlineStrong, lineWidth: 1)
            )
    }

    private func logoGrid(_ assets: [String], size: CGFloat) -> some View {
        let cell = size * 0.32
        let gap = size * 0.07
        return VStack(spacing: gap) {
            HStack(spacing: gap) {
                ProviderMark(mark: .asset(assets[0]), size: cell)
                ProviderMark(mark: .asset(assets[1]), size: cell)
            }
            HStack(spacing: gap) {
                ProviderMark(mark: .asset(assets[2]), size: cell)
                ProviderMark(mark: .asset(assets[3]), size: cell)
            }
        }
    }

    // MARK: - Choosing

    /// Marks the tapped option, lets the press read, then moves on.
    private func choose(_ id: String, then action: @escaping () -> Void) {
        guard chosen == nil, !busy else { return }
        errorMessage = nil
        withAnimation(.spring(response: 0.28, dampingFraction: 0.68)) { chosen = id }
        Task {
            try? await Task.sleep(for: .milliseconds(190))
            action()
            try? await Task.sleep(for: .milliseconds(650))
            chosen = nil
        }
    }

    /// Chevron at rest, a filled tick once the row is chosen.
    private func rowAccessory(_ on: Bool) -> some View {
        ZStack {
            Image(systemName: "chevron.right")
                .font(.system(size: 13, weight: .semibold))
                .foregroundStyle(WTheme.inkTertiary)
                .opacity(on ? 0 : 1)
                .scaleEffect(on ? 0.5 : 1)
            Image(systemName: "checkmark")
                .font(.system(size: 11, weight: .bold))
                .foregroundStyle(WTheme.inkInverse)
                .frame(width: 22, height: 22)
                .background(WTheme.ink, in: Circle())
                .scaleEffect(on ? 1 : 0.3)
                .opacity(on ? 1 : 0)
        }
        .frame(width: 22)
        .animation(.spring(response: 0.28, dampingFraction: 0.62), value: on)
    }

    private func choiceRow(
        _ mark: String,
        title: String,
        sub: String,
        pill: String? = nil,
        index: Int,
        action: @escaping () -> Void
    ) -> some View {
        let on = chosen == mark
        return Button {
            choose(mark, then: action)
        } label: {
            HStack(spacing: 14) {
                markView(mark, size: 44)
                    .matchedGeometryEffect(id: mark, in: hero)
                    .scaleEffect(on ? 1.08 : 1)
                VStack(alignment: .leading, spacing: 3) {
                    if let pill {
                        Text(pill)
                            .font(.system(size: 11, weight: .semibold))
                            .foregroundStyle(WTheme.accent)
                            .padding(.horizontal, 7)
                            .padding(.vertical, 2.5)
                            .background(WTheme.accentSoft, in: Capsule())
                            .padding(.bottom, 2)
                    }
                    Text(title)
                        .font(.system(size: 16, weight: .semibold))
                        .foregroundStyle(WTheme.ink)
                    Text(sub)
                        .font(.system(size: 13.5))
                        .foregroundStyle(WTheme.inkSecondary)
                        .multilineTextAlignment(.leading)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer(minLength: 8)
                rowAccessory(on)
            }
            .padding(14)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(on ? WTheme.fill : WTheme.raised, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: 18, style: .continuous)
                    .strokeBorder(on ? WTheme.ink : WTheme.hairlineStrong, lineWidth: on ? 1.5 : 1)
            )
            .contentShape(RoundedRectangle(cornerRadius: 18, style: .continuous))
        }
        .buttonStyle(TapScaleStyle())
        .staggerIn(index)
    }

    /// A row for something this server cannot do, saying why instead of failing on tap.
    private func unavailableRow(_ mark: String, title: String, sub: String, index: Int) -> some View {
        HStack(spacing: 14) {
            markView(mark, size: 44).saturation(0).opacity(0.55)
            VStack(alignment: .leading, spacing: 3) {
                Text(title)
                    .font(.system(size: 16, weight: .semibold))
                    .foregroundStyle(WTheme.inkSecondary)
                Text(sub)
                    .font(.system(size: 13.5))
                    .foregroundStyle(WTheme.inkTertiary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(WTheme.surface, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
        .accessibilityElement(children: .combine)
        .staggerIn(index)
    }

    private func numberedSteps(_ steps: [String]) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            ForEach(Array(steps.enumerated()), id: \.offset) { index, text in
                HStack(alignment: .firstTextBaseline, spacing: 12) {
                    Text("\(index + 1)")
                        .font(.system(size: 12, weight: .semibold))
                        .monospacedDigit()
                        .foregroundStyle(WTheme.ink)
                        .frame(width: 22, height: 22)
                        .background(WTheme.fill, in: Circle())
                        .alignmentGuide(.firstTextBaseline) { $0[VerticalAlignment.center] + 5 }
                    Text(LocalizedStringKey(text))
                        .font(.system(size: 15))
                        .foregroundStyle(WTheme.ink)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .connectCard()
        .staggerIn(0)
    }

    private func linkButton(_ title: String, _ url: String) -> some View {
        Button {
            if let link = URL(string: url) { openURL(link) }
        } label: {
            HStack(spacing: 7) {
                Text(title)
                Image(systemName: "arrow.up.right")
                    .font(.system(size: 12, weight: .semibold))
            }
        }
        .buttonStyle(OutlineButtonStyle(size: .regular))
    }

    private func note(_ text: String) -> some View {
        Text(text)
            .font(.system(size: 13))
            .foregroundStyle(WTheme.inkTertiary)
            .fixedSize(horizontal: false, vertical: true)
    }

    private func sectionLabel(_ text: String) -> some View {
        Text(text)
            .font(.system(size: 13, weight: .semibold))
            .foregroundStyle(WTheme.inkTertiary)
            .padding(.horizontal, 4)
    }

    private func loadingCard(_ text: String) -> some View {
        HStack(spacing: 12) {
            ProgressView()
            Text(text)
                .font(.system(size: 14.5))
                .foregroundStyle(WTheme.inkSecondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .connectCard()
    }

    private func errorCard(_ text: String, retry: @escaping () -> Void) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(text)
                .font(.system(size: 14))
                .foregroundStyle(WTheme.negative)
                .fixedSize(horizontal: false, vertical: true)
            Button("Try again", action: retry)
                .buttonStyle(OutlineButtonStyle(size: .regular))
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .connectCard()
    }

    private func hairlineDivider(inset: CGFloat) -> some View {
        Rectangle().fill(WTheme.hairline).frame(height: 1).padding(.leading, inset)
    }

    // MARK: - Footer

    private var showsPrimary: Bool {
        ![.provider, .google, .grants, .vendors].contains(page)
    }

    private var footer: some View {
        VStack(spacing: 12) {
            if let errorMessage {
                Text(errorMessage)
                    .font(.system(size: 13.5))
                    .foregroundStyle(WTheme.negative)
                    .frame(maxWidth: .infinity)
                    .multilineTextAlignment(.center)
                    .fixedSize(horizontal: false, vertical: true)
                    .transition(.opacity.combined(with: .move(edge: .bottom)))
            }

            if busy, page == .appConnect || page == .smtp {
                Text("Checking the login with your provider before saving.")
                    .font(.system(size: 13))
                    .foregroundStyle(WTheme.inkTertiary)
                    .frame(maxWidth: .infinity)
                    .multilineTextAlignment(.center)
                    .transition(.opacity)
            }

            if showsPrimary {
                Button {
                    Task { await primaryAction() }
                } label: {
                    Group {
                        if primaryBusy {
                            ProgressView().tint(WTheme.inkInverse)
                        } else {
                            HStack(spacing: 8) {
                                Text(primaryLabel)
                                    .contentTransition(.numericText())
                                if let icon = primaryIcon {
                                    Image(systemName: icon)
                                        .font(.system(size: 13.5, weight: .medium))
                                }
                            }
                        }
                    }
                }
                .buttonStyle(InkButtonStyle(size: .large, fullWidth: true))
                .disabled(!canAdvance)
                .allowsHitTesting(!primaryBusy)
                .animation(.easeOut(duration: 0.18), value: canAdvance)
                .animation(.snappy, value: primaryLabel)
                .transition(.move(edge: .bottom).combined(with: .opacity))
            }

            if page == .done, warmupOffer == .offer {
                Button("Skip for now", action: onClose)
                    .font(.system(size: 14, weight: .medium))
                    .foregroundStyle(WTheme.inkSecondary)
                    .disabled(primaryBusy)
                    .transition(.opacity)
            }
        }
        .padding(.top, 10)
        .padding(.bottom, 14)
        .animation(.spring(response: 0.42, dampingFraction: 0.86), value: showsPrimary)
        .animation(.easeOut(duration: 0.2), value: errorMessage != nil)
        .animation(.easeOut(duration: 0.2), value: warmupOffer)
    }

    private var primaryBusy: Bool { busy || warmupOffer == .starting }

    private var importCount: Int {
        guard let s = preview?.summary else { return 0 }
        return s.ready + s.needsSignin + s.existing
    }

    private func mailboxes(_ n: Int) -> String { "\(n) \(n == 1 ? "mailbox" : "mailboxes")" }

    private var primaryLabel: String {
        switch page {
        case .twoStep: return "It's on, continue"
        case .createPassword: return "I have it, continue"
        case .account, .imap: return "Continue"
        case .appConnect, .smtp: return "Connect mailbox"
        case .users, .vendorMailboxes:
            return picked.isEmpty ? "Pick mailboxes" : "Connect \(mailboxes(picked.count))"
        case .vendorKey: return vendorConnection == nil ? "Connect \(vendorEntry?.label ?? "vendor")" : "Save the new key"
        case .fileSource: return "Review"
        case .fileReview: return importCount == 0 ? "Nothing to import" : "Import \(mailboxes(importCount))"
        case .running: return "Done"
        case .done:
            switch warmupOffer {
            case .offer, .starting: return "Start warmup"
            case .active, .failed: return "Done"
            }
        case .provider, .google, .grants, .vendors: return ""
        }
    }

    private var primaryIcon: String? {
        switch page {
        case .twoStep, .createPassword, .account, .imap, .fileSource: return "arrow.right"
        case .done: return warmupOffer == .offer ? "flame.fill" : nil
        default: return nil
        }
    }

    // MARK: - Provider

    private var googleGrantAvailable: Bool {
        grantConfig?.googleEnabled == true || (grants ?? []).contains { $0.provider == .google }
    }

    private var microsoftAvailable: Bool {
        grantConfig?.microsoftEnabled == true || (grants ?? []).contains { $0.provider == .microsoft }
    }

    private var providerPage: some View {
        VStack(alignment: .leading, spacing: 0) {
            header("home", "Add a mailbox", "Pick who runs the inbox you send from, or bring in many at once.")

            VStack(spacing: 10) {
                choiceRow("google", title: "Google", sub: "Gmail and Google Workspace", index: 0) {
                    navigate(to: .google, route: .undecided)
                }

                if microsoftAvailable {
                    choiceRow("microsoft", title: "Microsoft", sub: "Microsoft 365, the whole organization at once", index: 1) {
                        grantProvider = .microsoft
                        navigate(to: .grants, route: .grant)
                    }
                } else {
                    unavailableRow(
                        "microsoft",
                        title: "Microsoft",
                        sub: sourcesLoaded
                            ? "Connects through an admin approval, which isn't set up on this server."
                            : "Checking what this server supports…",
                        index: 1
                    )
                }

                choiceRow("other", title: "Other provider", sub: "Zoho, Yahoo, iCloud, Fastmail or any IMAP server", index: 2) {
                    navigate(to: .account, route: .other)
                }
            }

            sectionLabel("Many at once")
                .padding(.top, 24)
                .padding(.bottom, 10)
                .staggerIn(3)

            VStack(spacing: 10) {
                choiceRow("file", title: "Import mailboxes", sub: "A CSV, a spreadsheet or a pasted list. Server settings are found for you.", index: 4) {
                    fileFromGoogle = false
                    navigate(to: .fileSource, route: .file)
                }
                if catalog != nil {
                    choiceRow("vendor", title: "Inbox vendor", sub: "InboxKit, Zapmail, Mailforge and more. Paste an API key, pick mailboxes.", index: 5) {
                        navigate(to: .vendors, route: .vendor)
                    }
                    .transition(.opacity)
                }
            }

            if !cloudMailboxes.isEmpty {
                cloudSection
                    .padding(.top, 24)
                    .staggerIn(6)
            }

            HStack(spacing: 7) {
                Image(systemName: "lock")
                    .font(.system(size: 11, weight: .medium))
                Text("Passwords and keys are stored encrypted.")
                    .font(.system(size: 13))
            }
            .foregroundStyle(WTheme.inkTertiary)
            .padding(.top, 18)
            .staggerIn(7)
        }
    }

    private var cloudSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 6) {
                Image(systemName: "cloud.fill")
                    .font(.system(size: 12))
                    .foregroundStyle(WTheme.accent)
                Text("On your Warmbly Cloud workspace")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(WTheme.inkTertiary)
            }
            .padding(.horizontal, 4)
            VStack(spacing: 0) {
                ForEach(Array(cloudMailboxes.enumerated()), id: \.element.id) { index, box in
                    if index > 0 { hairlineDivider(inset: 60) }
                    HStack(spacing: 12) {
                        MailboxAvatar(email: box.email, avatarURL: nil, brand: MailboxBrand(provider: box.provider, mailHost: nil), seed: box.id, size: 34, ring: WTheme.raised)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(box.email)
                                .font(.system(size: 14.5, weight: .medium))
                                .foregroundStyle(WTheme.ink)
                                .lineLimit(1)
                                .truncationMode(.middle)
                            Text("Connected on the cloud")
                                .font(.system(size: 12.5))
                                .foregroundStyle(WTheme.inkSecondary)
                        }
                        Spacer(minLength: 8)
                        Button {
                            Task { await adopt(box) }
                        } label: {
                            if adopting == box.id {
                                ProgressView().controlSize(.small)
                            } else {
                                Text("Add")
                            }
                        }
                        .buttonStyle(OutlineButtonStyle(size: .small))
                        .disabled(adopting != nil)
                    }
                    .padding(.horizontal, 14)
                    .padding(.vertical, 11)
                }
            }
            .connectCard()
        }
    }

    // MARK: - Google

    private var googlePage: some View {
        VStack(alignment: .leading, spacing: 0) {
            header("google", "Connect Google", googleGrantAvailable
                ? "Pick how Warmbly signs in. Personal @gmail.com addresses use an app password."
                : "One mailbox at a time, with an app password.")

            VStack(spacing: 10) {
                if googleGrantAvailable {
                    choiceRow(
                        "grant",
                        title: "Whole Workspace domain",
                        sub: "A super admin authorizes Warmbly once, then you pick the mailboxes. No app passwords.",
                        pill: "Recommended",
                        index: 0
                    ) {
                        grantProvider = .google
                        navigate(to: .grants, route: .grant)
                    }
                } else if !sourcesLoaded {
                    loadingCard("Checking what this server supports…").staggerIn(0)
                }

                choiceRow(
                    "apppassword",
                    title: "App password",
                    sub: "One mailbox, in about two minutes. Works for any Gmail or Workspace account.",
                    index: 1
                ) {
                    navigate(to: .twoStep, route: .appPassword)
                }
            }

            HStack(spacing: 4) {
                Text("Have a list of app passwords?")
                    .foregroundStyle(WTheme.inkTertiary)
                Button("Import them") {
                    fileFromGoogle = true
                    navigate(to: .fileSource, route: .file)
                }
                .foregroundStyle(WTheme.accent)
            }
            .font(.system(size: 13.5, weight: .medium))
            .padding(.top, 16)
            .padding(.horizontal, 4)
            .staggerIn(2)
        }
    }

    // MARK: - Google: app password

    private var twoStepPage: some View {
        VStack(alignment: .leading, spacing: 0) {
            header("apppassword", "Turn on 2-Step Verification", "Google only issues app passwords on accounts that have it on.")
            numberedSteps([
                "Open the Google account's security page and find **2-Step Verification** under \"How you sign in to Google\".",
                "Turn it on with a phone prompt, an authenticator app or a text message. A security key alone is not enough.",
            ])
            linkButton("Open Google security", "https://myaccount.google.com/security")
                .padding(.top, 16)
                .staggerIn(1)
            note("Already on? Just continue. On Google Workspace an admin can require or block it, and an account in Advanced Protection can't have an app password at all.")
                .padding(.top, 16)
                .staggerIn(2)
        }
    }

    private var createPasswordPage: some View {
        VStack(alignment: .leading, spacing: 0) {
            header("apppassword", "Create an app password", "A 16-letter password Google issues for one app.")
            numberedSteps([
                "Open the app passwords page. Google asks you to sign in again.",
                "Type **Warmbly** as the app name and tap **Create**.",
                "Copy the 16 letters it shows. Google shows them once; the spaces don't matter.",
            ])
            linkButton("Open app passwords", "https://myaccount.google.com/apppasswords")
                .padding(.top, 16)
                .staggerIn(1)
            note("No such page? 2-Step Verification is still off, or a Workspace admin has blocked app passwords.")
                .padding(.top, 16)
                .staggerIn(2)
        }
    }

    private var cleanedAppPassword: String { appPassword.filter { !$0.isWhitespace } }

    private var appConnectPage: some View {
        VStack(alignment: .leading, spacing: 0) {
            header("apppassword", "Connect the mailbox", "Your address and the app password. Nothing else to type.")

            VStack(alignment: .leading, spacing: 14) {
                fieldBlock("Name") {
                    styledField("Alex Rivera", text: $name, field: .name) { focused = .email }
                        .textContentType(.name)
                }
                .staggerIn(0)
                fieldBlock("Email") {
                    styledField("alex@gmail.com", text: $email, field: .email) { focused = .appPassword }
                        .textContentType(.emailAddress)
                        .keyboardType(.emailAddress)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                }
                .staggerIn(1)
                fieldBlock("App password") {
                    styledField("xxxx xxxx xxxx xxxx", text: $appPassword, field: .appPassword, secure: true, submit: .done) {
                        Task { await primaryAction() }
                    }
                }
                .staggerIn(2)
                if !cleanedAppPassword.isEmpty, cleanedAppPassword.count != 16 {
                    Text("Google app passwords are 16 letters. This looks like something else, maybe the account password.")
                        .font(.system(size: 13))
                        .foregroundStyle(WTheme.warning)
                        .fixedSize(horizontal: false, vertical: true)
                        .transition(.opacity)
                }
            }
            .animation(.easeOut(duration: 0.2), value: cleanedAppPassword.count == 16)

            serverSettingsCard(
                smtp: (KnownHost.gmail.smtpHost, KnownHost.gmail.smtpPort),
                imap: (KnownHost.gmail.imapHost, 993),
                footnote: "The address must be the one the Google account signs in with, not an alias. Deleting the app password in Google disconnects the mailbox."
            )
            .padding(.top, 20)
            .staggerIn(3)
        }
    }

    private func serverSettingsCard(smtp: (String, Int), imap: (String, Int), footnote: String) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 6) {
                Image(systemName: "checkmark.seal.fill")
                    .font(.system(size: 12))
                    .foregroundStyle(WTheme.positive)
                Text("Server settings, set for you")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(WTheme.ink)
            }
            .padding(.bottom, 10)
            serverRow("SMTP", smtp.0, smtp.1)
            Rectangle().fill(WTheme.hairline).frame(height: 1).padding(.vertical, 8)
            serverRow("IMAP", imap.0, imap.1)
            Text(footnote)
                .font(.system(size: 12.5))
                .foregroundStyle(WTheme.inkTertiary)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.top, 12)
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .connectCard()
    }

    private func serverRow(_ label: String, _ host: String, _ port: Int) -> some View {
        HStack(spacing: 10) {
            Text(label)
                .font(.system(size: 12, weight: .semibold))
                .foregroundStyle(WTheme.inkSecondary)
                .frame(width: 38, alignment: .leading)
            Text(host)
                .font(.system(size: 13.5, design: .monospaced))
                .foregroundStyle(WTheme.ink)
                .lineLimit(1)
                .truncationMode(.middle)
            Text(":\(port)")
                .font(.system(size: 13.5, design: .monospaced))
                .foregroundStyle(WTheme.inkSecondary)
            Spacer(minLength: 4)
            Text(Self.security(forPort: port) == "tls" ? "SSL/TLS" : "STARTTLS")
                .font(.system(size: 11.5, weight: .medium))
                .foregroundStyle(WTheme.inkTertiary)
        }
    }

    // MARK: - Other provider

    private var knownHost: KnownHost? { KnownHost.forEmail(trimmedEmail) }

    private var accountPage: some View {
        VStack(alignment: .leading, spacing: 0) {
            header("other", "Your sending address", "The name and email recipients will see.")

            VStack(alignment: .leading, spacing: 14) {
                fieldBlock("Name") {
                    styledField("Alex Rivera", text: $name, field: .name) { focused = .email }
                        .textContentType(.name)
                }
                .staggerIn(0)
                fieldBlock("Email") {
                    styledField("alex@company.com", text: $email, field: .email, submit: .continue) {
                        Task { await primaryAction() }
                    }
                    .textContentType(.emailAddress)
                    .keyboardType(.emailAddress)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                }
                .staggerIn(1)
                if let host = knownHost {
                    knownHostCard(host)
                        .transition(.opacity.combined(with: .move(edge: .top)))
                }
            }
            .animation(.spring(response: 0.4, dampingFraction: 0.86), value: knownHost)
        }
        .onAppear {
            if name.isEmpty { focusSoon(.name) }
        }
    }

    private func knownHostCard(_ host: KnownHost) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 10) {
                ProviderMark(mark: host.mark, size: 22)
                Text(host.refusal == nil ? "\(host.label): server settings filled in" : host.label)
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundStyle(WTheme.ink)
            }
            if let refusal = host.refusal {
                Text(refusal)
                    .font(.system(size: 13.5))
                    .foregroundStyle(WTheme.inkSecondary)
                    .fixedSize(horizontal: false, vertical: true)
            } else if let url = host.appPasswordURL {
                Text("\(host.label) needs an app password, not your main login.")
                    .font(.system(size: 13.5))
                    .foregroundStyle(WTheme.inkSecondary)
                    .fixedSize(horizontal: false, vertical: true)
                linkButton("Create an app password", url)
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(host.refusal == nil ? WTheme.raised : Tone.amber.background, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 18, style: .continuous)
                .strokeBorder(host.refusal == nil ? WTheme.hairlineStrong : .clear, lineWidth: 1)
        )
    }

    private var imapPage: some View {
        VStack(alignment: .leading, spacing: 0) {
            header("other", "Incoming mail", "IMAP keeps replies and inbox activity in sync.")

            VStack(alignment: .leading, spacing: 14) {
                HStack(alignment: .top, spacing: 12) {
                    fieldBlock("Host") {
                        styledField("imap.example.com", text: $imapHost, field: .imapHost) { focused = .imapUser }
                            .keyboardType(.URL)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                    }
                    fieldBlock("Port") {
                        styledField("993", text: $imapPort, field: .imapPort)
                            .keyboardType(.numberPad)
                            .frame(width: 92)
                    }
                }
                .staggerIn(0)
                fieldBlock("Username") {
                    styledField("alex@company.com", text: $imapUsername, field: .imapUser) { focused = .imapPass }
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                }
                .staggerIn(1)
                fieldBlock("Password", helper: knownHost?.appPasswordURL != nil
                    ? "The app password from \(knownHost?.label ?? "your provider")."
                    : "Usually an app password, not your main login.") {
                    styledField("App password", text: $imapPassword, field: .imapPass, secure: true, submit: .continue) {
                        Task { await primaryAction() }
                    }
                }
                .staggerIn(2)
            }
        }
        .onAppear {
            if imapUsername.isEmpty { imapUsername = trimmedEmail }
            focusSoon(imapHost.isEmpty ? .imapHost : .imapPass)
        }
    }

    private var smtpPage: some View {
        VStack(alignment: .leading, spacing: 0) {
            header("other", "Outgoing mail", "The SMTP server your campaigns send through.")

            VStack(alignment: .leading, spacing: 14) {
                HStack(alignment: .top, spacing: 12) {
                    fieldBlock("Host") {
                        styledField("smtp.example.com", text: $smtpHost, field: .smtpHost) { focused = .smtpPort }
                            .keyboardType(.URL)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                    }
                    fieldBlock("Port", helper: "465 or 587") {
                        styledField("587", text: $smtpPort, field: .smtpPort)
                            .keyboardType(.numberPad)
                            .frame(width: 92)
                    }
                }
                .staggerIn(0)

                Toggle(isOn: $sameLogin.animation(.spring(response: 0.4, dampingFraction: 0.85))) {
                    Text("Use the same login as IMAP")
                        .font(.system(size: 15, weight: .medium))
                        .foregroundStyle(WTheme.ink)
                }
                .tint(WTheme.ink)
                .padding(.horizontal, 16)
                .frame(height: 54)
                .connectCard(radius: 14)
                .staggerIn(1)

                if !sameLogin {
                    fieldBlock("Username") {
                        styledField("alex@company.com", text: $smtpUsername, field: .smtpUser) { focused = .smtpPass }
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                    }
                    .transition(.opacity.combined(with: .move(edge: .top)))
                    fieldBlock("Password") {
                        styledField("App password", text: $smtpPassword, field: .smtpPass, secure: true, submit: .continue) {
                            Task { await primaryAction() }
                        }
                    }
                    .transition(.opacity.combined(with: .move(edge: .top)))
                }
            }
        }
        .onAppear {
            if smtpHost.isEmpty { focusSoon(.smtpHost) }
        }
    }

    // MARK: - Admin grant

    private var grantMark: String { grantProvider == .google ? "grant" : "microsoft" }

    private var providerGrants: [MailboxGrant] {
        (grants ?? []).filter { $0.provider == grantProvider }
    }

    private var grantsPage: some View {
        let list = providerGrants
        let enabled = grantConfig?.enabled(grantProvider) == true
        let noun = grantProvider == .google ? "domain" : "organization"
        return VStack(alignment: .leading, spacing: 0) {
            header(
                grantMark,
                grantProvider == .google ? "Whole Workspace domain" : "Whole organization",
                list.isEmpty ? "Every mailbox on it connects at once, with no passwords." : "Pick the \(noun) to connect mailboxes from."
            )

            if !list.isEmpty {
                VStack(spacing: 0) {
                    ForEach(Array(list.enumerated()), id: \.element.id) { index, item in
                        if index > 0 { hairlineDivider(inset: 66) }
                        grantRow(item)
                    }
                }
                .connectCard()
                .clipShape(RoundedRectangle(cornerRadius: 18, style: .continuous))
                .padding(.bottom, 16)
                .staggerIn(0)
            }

            Group {
                if !enabled {
                    notSetUpCard
                } else if list.isEmpty {
                    setUpOnDashboardCard
                } else {
                    note("To add another \(noun), set it up from the dashboard on a computer.")
                        .padding(.horizontal, 4)
                }
            }
            .staggerIn(1)
        }
    }

    private func grantRow(_ item: MailboxGrant) -> some View {
        let count = item.mailboxes ?? 0
        let on = chosen == "grant-\(item.id)"
        return Button {
            choose("grant-\(item.id)") { openGrant(item) }
        } label: {
            HStack(alignment: .top, spacing: 12) {
                frameTile(40) { ProviderMark(mark: item.provider.mark, size: 20) }
                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 8) {
                        Text(item.name)
                            .font(.system(size: 15.5, weight: .semibold))
                            .foregroundStyle(WTheme.ink)
                            .lineLimit(1)
                        StatusPill(text: item.isActive ? "active" : "needs a check", tone: item.isActive ? .emerald : .rose)
                    }
                    Text("\(mailboxes(count)) connected")
                        .font(.system(size: 13))
                        .foregroundStyle(WTheme.inkSecondary)
                    if let admin = item.adminEmail, !admin.isEmpty {
                        Text("Authorized by \(admin)")
                            .font(.system(size: 12.5))
                            .foregroundStyle(WTheme.inkTertiary)
                            .lineLimit(1)
                            .truncationMode(.middle)
                    }
                    if !item.isActive {
                        Text(item.lastError ?? "The grant failed its last check. Its mailboxes are stopped until it works again.")
                            .font(.system(size: 13))
                            .foregroundStyle(WTheme.negative)
                            .fixedSize(horizontal: false, vertical: true)
                        Button {
                            Task { await checkGrant(item) }
                        } label: {
                            HStack(spacing: 6) {
                                if checkingGrant == item.id {
                                    ProgressView().controlSize(.mini)
                                } else {
                                    Image(systemName: "arrow.clockwise")
                                }
                                Text("Check again")
                            }
                        }
                        .buttonStyle(OutlineButtonStyle(size: .small))
                        .disabled(checkingGrant != nil)
                        .padding(.top, 4)
                    }
                }
                Spacer(minLength: 4)
                if item.isActive {
                    rowAccessory(on).padding(.top, 9)
                }
            }
            .padding(14)
            .background(on ? WTheme.fill : .clear)
            .contentShape(Rectangle())
        }
        .buttonStyle(MoreRowPressStyle())
        .disabled(!item.isActive)
    }

    private var setUpOnDashboardCard: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 12) {
                IconTile(symbol: "laptopcomputer", tone: .sky, size: 40)
                Text("Set it up once from a computer")
                    .font(.system(size: 16, weight: .semibold))
                    .foregroundStyle(WTheme.ink)
            }
            Text(grantProvider == .google
                ? "A super admin authorizes Warmbly in the Google Admin console and proves the domain from the dashboard. After that, every mailbox on it connects from here."
                : "A Global Administrator approves Warmbly for the organization from the dashboard. After that, every mailbox in it connects from here.")
                .font(.system(size: 14.5))
                .foregroundStyle(WTheme.inkSecondary)
                .fixedSize(horizontal: false, vertical: true)
            if let url = AppConfig.dashboardURL("/app/emails") {
                Button {
                    openURL(url)
                } label: {
                    HStack(spacing: 7) {
                        Text("Open the dashboard")
                        Image(systemName: "arrow.up.right")
                            .font(.system(size: 12, weight: .semibold))
                    }
                }
                .buttonStyle(OutlineButtonStyle(size: .regular))
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .connectCard()
    }

    private var notSetUpCard: some View {
        let missing = grantConfig?.missing(grantProvider) ?? []
        return VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 12) {
                IconTile(symbol: "gearshape.fill", tone: .amber, size: 40)
                Text("Not set up on this server")
                    .font(.system(size: 16, weight: .semibold))
                    .foregroundStyle(WTheme.ink)
            }
            Text("Admin connections need settings only the person running this instance can add.")
                .font(.system(size: 14.5))
                .foregroundStyle(WTheme.inkSecondary)
                .fixedSize(horizontal: false, vertical: true)
            if !missing.isEmpty {
                VStack(alignment: .leading, spacing: 6) {
                    ForEach(missing, id: \.self) { key in
                        Text(key)
                            .font(.system(size: 12.5, design: .monospaced))
                            .foregroundStyle(WTheme.ink)
                            .padding(.horizontal, 8)
                            .padding(.vertical, 4)
                            .background(WTheme.fill, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
                    }
                }
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .connectCard()
    }

    private var usersPage: some View {
        VStack(alignment: .leading, spacing: 0) {
            header(
                grantMark,
                "Pick mailboxes",
                "Accounts on \(grant?.name ?? "this domain"). They connect through the grant: no passwords, nothing for each person to approve."
            )
            pickList(
                items: (users ?? []).map(PickItem.init),
                loading: users == nil,
                loadingText: grantProvider == .google ? "Asking Google for your directory…" : "Asking Microsoft for your organization's users…",
                retry: { Task { await loadUsers() } }
            )
        }
    }

    // MARK: - Inbox vendor

    private var vendorsPage: some View {
        VStack(alignment: .leading, spacing: 0) {
            header("vendor", "Inbox vendor", "Paste an API key from your vendor, then pick the mailboxes. Their logins come straight from the vendor.")

            if !vendorConnections.isEmpty {
                sectionLabel("Your vendor accounts")
                    .padding(.bottom, 10)
                    .staggerIn(0)
                VStack(spacing: 0) {
                    ForEach(Array(vendorConnections.enumerated()), id: \.element.id) { index, conn in
                        if index > 0 { hairlineDivider(inset: 66) }
                        vendorConnectionRow(conn)
                    }
                }
                .connectCard()
                .clipShape(RoundedRectangle(cornerRadius: 18, style: .continuous))
                .padding(.bottom, 24)
                .staggerIn(1)
            }

            sectionLabel(vendorConnections.isEmpty ? "Pick your vendor" : "Connect another account")
                .padding(.bottom, 10)
                .staggerIn(2)
            LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: 10), count: 3), spacing: 10) {
                ForEach(Array((catalog ?? []).enumerated()), id: \.element.id) { index, entry in
                    vendorTile(entry)
                        .staggerIn(3 + index)
                }
            }
        }
    }

    private func vendorTile(_ entry: VendorCatalogEntry) -> some View {
        let mark = "vendor-\(entry.id)"
        let on = chosen == mark
        return Button {
            choose(mark) { openVendorKey(entry, updating: nil) }
        } label: {
            VStack(spacing: 10) {
                markView(mark, size: 46)
                    .matchedGeometryEffect(id: mark, in: hero)
                    .scaleEffect(on ? 1.1 : 1)
                Text(entry.label)
                    .font(.system(size: 13, weight: .medium))
                    .foregroundStyle(WTheme.ink)
                    .lineLimit(1)
                    .minimumScaleFactor(0.8)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 14)
            .background(on ? WTheme.fill : WTheme.raised, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: 18, style: .continuous)
                    .strokeBorder(on ? WTheme.ink : WTheme.hairlineStrong, lineWidth: on ? 1.5 : 1)
            )
            .contentShape(RoundedRectangle(cornerRadius: 18, style: .continuous))
        }
        .buttonStyle(TapScaleStyle())
    }

    private func vendorConnectionRow(_ conn: VendorConnection) -> some View {
        let on = chosen == "conn-\(conn.id)"
        let count = conn.mailboxes ?? 0
        return Button {
            choose("conn-\(conn.id)") {
                if conn.isActive {
                    openVendorConnection(conn)
                } else if let entry = (catalog ?? []).first(where: { $0.id == conn.vendor }) {
                    openVendorKey(entry, updating: conn)
                }
            }
        } label: {
            HStack(alignment: .top, spacing: 12) {
                markView("vendor-\(conn.vendor)", size: 40)
                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 8) {
                        Text(conn.label)
                            .font(.system(size: 15.5, weight: .semibold))
                            .foregroundStyle(WTheme.ink)
                            .lineLimit(1)
                        StatusPill(text: conn.isActive ? "active" : "key refused", tone: conn.isActive ? .emerald : .rose)
                    }
                    Text("\(mailboxes(count)) connected")
                        .font(.system(size: 13))
                        .foregroundStyle(WTheme.inkSecondary)
                    if !conn.isActive {
                        Text(conn.lastError ?? "The vendor no longer accepts this key. Tap to replace it.")
                            .font(.system(size: 13))
                            .foregroundStyle(WTheme.negative)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                Spacer(minLength: 4)
                rowAccessory(on).padding(.top, 9)
            }
            .padding(14)
            .background(on ? WTheme.fill : .clear)
            .contentShape(Rectangle())
        }
        .buttonStyle(MoreRowPressStyle())
    }

    private var vendorKeyPage: some View {
        let entry = vendorEntry
        let label = entry?.label ?? "your vendor"
        return VStack(alignment: .leading, spacing: 0) {
            header(
                "vendor-\(entry?.id ?? "")",
                vendorConnection == nil ? "Connect \(label)" : "Replace the key",
                vendorConnection == nil
                    ? "Checked with \(label) before it's saved. Stored encrypted and never shown again."
                    : "The new key is checked with \(label) before it replaces the old one."
            )

            VStack(alignment: .leading, spacing: 14) {
                if vendorConnection == nil {
                    fieldBlock("Account name", helper: "Optional. Tells several \(label) accounts apart.") {
                        styledField(label, text: $vendorLabel, field: .vendorLabel)
                    }
                    .staggerIn(0)
                }
                ForEach(Array((entry?.fields ?? []).enumerated()), id: \.element.key) { index, field in
                    fieldBlock(field.label, helper: field.help) {
                        styledField(
                            field.required ? field.label : "\(field.label) (optional)",
                            text: Binding(
                                get: { vendorValues[field.key] ?? "" },
                                set: { vendorValues[field.key] = $0 }
                            ),
                            field: .vendorField(field.key),
                            secure: field.secret,
                            submit: .done
                        ) {
                            Task { await primaryAction() }
                        }
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    }
                    .staggerIn(index + 1)
                }
                if let help = entry?.keyHelpURL {
                    linkButton("Where to find it", help)
                        .staggerIn(4)
                }
            }
        }
    }

    private var vendorMailboxesPage: some View {
        let conn = vendorConnection
        let boxes = vendorMailboxes ?? []
        let microsoft = boxes.filter { $0.provider == "microsoft" && !$0.connected }.count
        let manyWorkspaces = Set(boxes.compactMap(\.workspace)).count > 1
        return VStack(alignment: .leading, spacing: 0) {
            header(
                "vendor-\(conn?.vendor ?? vendorEntry?.id ?? "")",
                "Pick mailboxes",
                "In \(conn?.label ?? "this account"). Each login is read from the vendor while the import runs."
            )
            if microsoft > 0 {
                HStack(alignment: .top, spacing: 10) {
                    Image(systemName: "info.circle.fill")
                        .foregroundStyle(WTheme.accent)
                    Text("\(microsoft) Microsoft \(microsoft == 1 ? "mailbox needs" : "mailboxes need") a Microsoft sign-in after the import, unless an admin grant covers the domain. Finish those on the dashboard.")
                        .font(.system(size: 13.5))
                        .foregroundStyle(WTheme.ink)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .padding(14)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(WTheme.accentSoft, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
                .padding(.bottom, 14)
                .staggerIn(0)
            }
            pickList(
                items: boxes.map { PickItem($0, showWorkspace: manyWorkspaces) },
                loading: vendorMailboxes == nil,
                loadingText: "Reading the mailboxes in \(conn?.label ?? "your account")…",
                retry: { Task { await loadVendorMailboxes() } }
            )
        }
    }

    // MARK: - File import

    private static let importTypes: [UTType] = [
        .commaSeparatedText, .tabSeparatedText, .plainText,
        UTType(filenameExtension: "xlsx") ?? .spreadsheet,
    ]

    private var fileSourcePage: some View {
        VStack(alignment: .leading, spacing: 0) {
            header("file", "Import mailboxes", "A CSV or spreadsheet export, or a pasted list. Each domain's servers are found for you.")

            fileCard
                .staggerIn(0)

            if file == nil {
                HStack(spacing: 10) {
                    Rectangle().fill(WTheme.hairlineStrong).frame(height: 1)
                    Text("or paste a list")
                        .font(.system(size: 12.5, weight: .medium))
                        .foregroundStyle(WTheme.inkTertiary)
                        .fixedSize()
                    Rectangle().fill(WTheme.hairlineStrong).frame(height: 1)
                }
                .padding(.vertical, 18)
                .staggerIn(1)

                ZStack(alignment: .topLeading) {
                    if pasted.isEmpty {
                        Text(verbatim: "alex@acme.io, app-password\nmaya@acme.io, app-password")
                            .font(.system(size: 14.5, design: .monospaced))
                            .foregroundStyle(WTheme.inkTertiary)
                            .padding(.horizontal, 16)
                            .padding(.vertical, 14)
                            .allowsHitTesting(false)
                    }
                    TextEditor(text: $pasted)
                        .font(.system(size: 14.5, design: .monospaced))
                        .foregroundStyle(WTheme.ink)
                        .scrollContentBackground(.hidden)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .focused($focused, equals: .paste)
                        .padding(.horizontal, 11)
                        .padding(.vertical, 6)
                }
                .frame(minHeight: 140)
                .background(WTheme.raised, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
                .overlay(
                    RoundedRectangle(cornerRadius: 16, style: .continuous)
                        .strokeBorder(focused == .paste ? WTheme.ink : WTheme.hairlineStrong, lineWidth: focused == .paste ? 1.5 : 1)
                )
                .staggerIn(2)

                note("One mailbox per line: the address, then its password. A header row is recognized on its own.")
                    .padding(.top, 10)
                    .padding(.horizontal, 4)
                    .staggerIn(3)
            }
        }
        .animation(.spring(response: 0.4, dampingFraction: 0.86), value: file?.filename)
    }

    private var fileCard: some View {
        let has = file != nil
        return Button {
            pickingFile = true
        } label: {
            HStack(spacing: 14) {
                IconTile(symbol: has ? "doc.text.fill" : "doc.badge.plus", tone: .emerald, size: 44)
                VStack(alignment: .leading, spacing: 3) {
                    Text(file?.filename ?? "Choose a file")
                        .font(.system(size: 16, weight: .semibold))
                        .foregroundStyle(WTheme.ink)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Text(file.map { ByteCountFormatter.string(fromByteCount: Int64($0.data.count), countStyle: .file) + ", tap to change" }
                        ?? "CSV or XLSX. Vendor exports are recognized.")
                        .font(.system(size: 13.5))
                        .foregroundStyle(WTheme.inkSecondary)
                }
                Spacer(minLength: 44)
            }
            .padding(14)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(has ? Tone.emerald.background : WTheme.raised, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: 18, style: .continuous)
                    .strokeBorder(has ? Tone.emerald.color.opacity(0.35) : WTheme.hairlineStrong, style: StrokeStyle(lineWidth: 1, dash: has ? [] : [5, 4]))
            )
            .contentShape(RoundedRectangle(cornerRadius: 18, style: .continuous))
        }
        .buttonStyle(TapScaleStyle())
        .overlay(alignment: .trailing) {
            if has {
                Button {
                    withAnimation(.snappy) { file = nil }
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.system(size: 20))
                        .foregroundStyle(WTheme.inkTertiary)
                        .frame(width: 44, height: 44)
                }
                .buttonStyle(TapScaleStyle())
                .accessibilityLabel("Remove file")
                .padding(.trailing, 6)
                .transition(.scale.combined(with: .opacity))
            }
        }
    }

    private var fileReviewPage: some View {
        VStack(alignment: .leading, spacing: 0) {
            header("file", "Check the import", previewSubtitle)
            if let preview {
                reviewSummary(preview.summary)
                    .staggerIn(0)

                if !preview.mapsPassword {
                    fieldBlock("Password for every mailbox", helper: "The list has no password column, so every mailbox signs in with this one.") {
                        styledField("Shared password", text: $sharedPassword, field: .sharedPassword, secure: true, submit: .done)
                    }
                    .padding(.top, 18)
                    .staggerIn(1)
                }

                if let domains = preview.domains, !domains.isEmpty {
                    sectionLabel("Where they're hosted")
                        .padding(.top, 22)
                        .padding(.bottom, 10)
                        .staggerIn(2)
                    VStack(spacing: 0) {
                        ForEach(Array(domains.prefix(8).enumerated()), id: \.element.domain) { index, domain in
                            if index > 0 { hairlineDivider(inset: 52) }
                            domainRow(domain)
                        }
                    }
                    .connectCard()
                    .staggerIn(3)
                }

                if let issues = preview.issues, !issues.isEmpty {
                    VStack(alignment: .leading, spacing: 12) {
                        ForEach(issues, id: \.self) { issue in
                            HStack(alignment: .top, spacing: 10) {
                                Image(systemName: "exclamationmark.triangle.fill")
                                    .font(.system(size: 13))
                                    .foregroundStyle(WTheme.warning)
                                    .padding(.top, 2)
                                VStack(alignment: .leading, spacing: 3) {
                                    Text("\(issue.title) (\(issue.count))")
                                        .font(.system(size: 14, weight: .semibold))
                                        .foregroundStyle(WTheme.ink)
                                    Text(issue.fix)
                                        .font(.system(size: 13))
                                        .foregroundStyle(WTheme.inkSecondary)
                                        .fixedSize(horizontal: false, vertical: true)
                                }
                            }
                        }
                    }
                    .padding(16)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Tone.amber.background, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
                    .padding(.top, 16)
                    .staggerIn(4)
                }

                warmupToggle
                    .padding(.top, 16)
                    .staggerIn(5)
            } else {
                loadingCard("Reading the list and finding each domain's servers…")
            }
        }
    }

    private var previewSubtitle: String {
        guard let preview else { return "Nothing connects until you start." }
        let rows = "\(preview.summary.total) \(preview.summary.total == 1 ? "row" : "rows")"
        if let vendor = preview.vendor { return "A \(vendor.label) export, \(rows). Nothing connects until you start." }
        return "\(rows) read. Nothing connects until you start."
    }

    private struct Stat: Identifiable {
        var id: String { label }
        var label: String
        var value: Int
        var tone: Tone
    }

    private func reviewSummary(_ s: MailboxImportPreview.Summary) -> some View {
        let stats = [
            Stat(label: "Ready", value: s.ready, tone: .emerald),
            Stat(label: "Need a sign-in", value: s.needsSignin, tone: .sky),
            Stat(label: "Already here", value: s.existing, tone: .slate),
            Stat(label: "Can't import", value: s.invalid + s.duplicate, tone: .rose),
        ].filter { $0.value > 0 || $0.label == "Ready" }
        return HStack(alignment: .top, spacing: 0) {
            ForEach(Array(stats.enumerated()), id: \.element.id) { index, stat in
                if index > 0 {
                    Rectangle().fill(WTheme.hairline).frame(width: 1).padding(.vertical, 14)
                }
                VStack(alignment: .leading, spacing: 4) {
                    Text("\(stat.value)")
                        .displayFont(26)
                        .foregroundStyle(WTheme.ink)
                        .contentTransition(.numericText())
                    HStack(alignment: .firstTextBaseline, spacing: 5) {
                        Circle().fill(stat.tone.color).frame(width: 6, height: 6)
                        Text(stat.label)
                            .font(.system(size: 12.5))
                            .foregroundStyle(WTheme.inkSecondary)
                            .lineLimit(2)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 12)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .connectCard()
        .animation(.snappy, value: s.ready)
    }

    private func domainRow(_ domain: MailboxImportPreview.Domain) -> some View {
        let brand = MailboxBrand(provider: nil, mailHost: domain.mailHost)
        return HStack(spacing: 12) {
            ProviderMark(mark: brand.mark, size: 24)
                .frame(width: 26)
            VStack(alignment: .leading, spacing: 2) {
                Text(domain.domain)
                    .font(.system(size: 14.5, weight: .medium))
                    .foregroundStyle(WTheme.ink)
                    .lineLimit(1)
                Text([domain.label, mailboxes(domain.rows)].compactMap { $0 }.joined(separator: " · "))
                    .font(.system(size: 12.5))
                    .foregroundStyle(WTheme.inkSecondary)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 6)
            if domain.passwordAuth == "oauth_only" {
                StatusPill(text: "sign-in", tone: .sky)
            } else if domain.passwordAuth == "unsupported" {
                StatusPill(text: "unsupported", tone: .rose)
            } else if domain.passwordAuth == "app_password", let url = domain.appPasswordURL {
                Button("App password") { if let link = URL(string: url) { openURL(link) } }
                    .font(.system(size: 12.5, weight: .semibold))
                    .foregroundStyle(WTheme.accent)
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 11)
    }

    // MARK: - Picking mailboxes

    /// A mailbox offered by a grant's directory or a vendor account.
    private struct PickItem: Identifiable {
        var id: String
        var title: String
        var email: String
        var selectable: Bool
        var pill: String?
        var pillTone: Tone = .slate
        var hint: String?

        init(_ user: GrantDirectoryUser) {
            id = user.id
            title = user.name.isEmpty ? user.email : user.name
            email = user.email
            selectable = user.selectable
            if user.connected {
                pill = "connected"; pillTone = .emerald
            } else if !user.enabled {
                pill = "suspended"; pillTone = .amber
            }
            hint = user.upgrade == true && user.selectable ? "Moves here, keeping its history" : nil
        }

        init(_ box: VendorMailbox, showWorkspace: Bool) {
            let status = (box.status ?? "").lowercased()
            let bad = ["suspend", "disabl", "inactive", "error", "fail", "block", "delet", "cancel", "expired"].contains { status.contains($0) }
            id = box.id
            title = box.name.isEmpty ? box.email : box.name
            email = box.email
            selectable = !box.connected && !bad
            if box.connected {
                pill = "connected"; pillTone = .emerald
            } else if bad {
                pill = status.replacingOccurrences(of: "_", with: " "); pillTone = .amber
            }
            hint = showWorkspace ? box.workspace : nil
        }
    }

    @ViewBuilder
    private func pickList(items: [PickItem], loading: Bool, loadingText: String, retry: @escaping () -> Void) -> some View {
        if let listError {
            errorCard(listError, retry: retry)
        } else if loading {
            loadingCard(loadingText)
        } else {
            pickListBody(items)
        }
    }

    private func pickListBody(_ all: [PickItem]) -> some View {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        let visible = q.isEmpty ? all : all.filter { $0.email.lowercased().contains(q) || $0.title.lowercased().contains(q) }
        let selectable = visible.filter(\.selectable)
        let allPicked = !selectable.isEmpty && selectable.allSatisfy { picked.contains($0.id) }
        return VStack(alignment: .leading, spacing: 12) {
            if all.count > 8 {
                HStack(spacing: 8) {
                    Image(systemName: "magnifyingglass")
                        .font(.system(size: 14))
                        .foregroundStyle(WTheme.inkTertiary)
                    TextField("Search mailboxes", text: $query)
                        .font(.system(size: 15.5))
                        .focused($focused, equals: .search)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                }
                .padding(.horizontal, 14)
                .frame(height: 44)
                .background(WTheme.fill, in: Capsule())
                .staggerIn(0)
            }

            HStack {
                Text("\(selectable.count) available")
                    .font(.system(size: 13, weight: .medium))
                    .foregroundStyle(WTheme.inkSecondary)
                    .contentTransition(.numericText())
                Spacer()
                if !selectable.isEmpty {
                    Button(allPicked ? "Clear" : "Select all") {
                        withAnimation(.snappy) {
                            if allPicked {
                                selectable.forEach { picked.remove($0.id) }
                            } else {
                                selectable.forEach { picked.insert($0.id) }
                            }
                        }
                    }
                    .font(.system(size: 13.5, weight: .semibold))
                    .foregroundStyle(WTheme.accent)
                }
            }
            .padding(.horizontal, 4)
            .staggerIn(1)

            if visible.isEmpty {
                note(q.isEmpty ? "No mailboxes here yet." : "Nothing matches \"\(query)\".")
                    .padding(.horizontal, 4)
            } else {
                LazyVStack(spacing: 0) {
                    ForEach(Array(visible.enumerated()), id: \.element.id) { index, item in
                        if index > 0 { hairlineDivider(inset: 60) }
                        pickRow(item)
                    }
                }
                .connectCard()
                .clipShape(RoundedRectangle(cornerRadius: 18, style: .continuous))
                .staggerIn(2)
            }

            warmupToggle
                .padding(.top, 4)
                .staggerIn(3)
        }
    }

    private func pickRow(_ item: PickItem) -> some View {
        let on = picked.contains(item.id)
        return Button {
            guard item.selectable else { return }
            withAnimation(.snappy) {
                if on { picked.remove(item.id) } else { picked.insert(item.id) }
            }
        } label: {
            HStack(spacing: 12) {
                WAvatar(name: item.title, seed: item.email, size: 34)
                VStack(alignment: .leading, spacing: 2) {
                    Text(item.title)
                        .font(.system(size: 15, weight: .medium))
                        .foregroundStyle(item.selectable ? WTheme.ink : WTheme.inkSecondary)
                        .lineLimit(1)
                    Text(item.email)
                        .font(.system(size: 12.5))
                        .foregroundStyle(WTheme.inkSecondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    if let hint = item.hint {
                        Text(hint)
                            .font(.system(size: 11.5, weight: .medium))
                            .foregroundStyle(WTheme.accent)
                    }
                }
                Spacer(minLength: 8)
                if let pill = item.pill {
                    StatusPill(text: pill, tone: item.pillTone)
                } else {
                    PickSquare(on: on)
                }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 11)
            .background(on ? WTheme.fill.opacity(0.7) : .clear)
            .contentShape(Rectangle())
        }
        .buttonStyle(MoreRowPressStyle())
        .disabled(!item.selectable)
        .accessibilityAddTraits(on ? .isSelected : [])
    }

    private var warmupToggle: some View {
        Toggle(isOn: $warmupImported) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Start warmup on each")
                    .font(.system(size: 15, weight: .medium))
                    .foregroundStyle(WTheme.ink)
                Text("Starts at 10 a day and ramps automatically.")
                    .font(.system(size: 12.5))
                    .foregroundStyle(WTheme.inkSecondary)
            }
        }
        .tint(WTheme.ink)
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .connectCard(radius: 14)
    }

    // MARK: - Running import

    private var runningMark: String {
        switch route {
        case .grant: return grantMark
        case .vendor: return "vendor-\(vendorConnection?.vendor ?? vendorEntry?.id ?? "")"
        default: return "file"
        }
    }

    private var runningPage: some View {
        let current = job
        let total = current?.total ?? picked.count
        let finished = current?.finished ?? 0
        let running = current?.isRunning ?? true
        return VStack(alignment: .leading, spacing: 0) {
            header(
                running ? runningMark : "done",
                running ? "Connecting \(mailboxes(total))" : "All done",
                running
                    ? (route == .grant ? "Each one signs in through the grant." : "Each one signs in with its own login.") + " This keeps going if you close it."
                    : "They're in your mailbox list now."
            )

            VStack(alignment: .leading, spacing: 14) {
                HStack(alignment: .firstTextBaseline) {
                    Text("\(finished)")
                        .displayFont(38)
                        .foregroundStyle(WTheme.ink)
                        .contentTransition(.numericText())
                    Text("of \(total)")
                        .font(.system(size: 15))
                        .foregroundStyle(WTheme.inkSecondary)
                    Spacer()
                    if running {
                        ProgressView().controlSize(.small)
                    } else {
                        Image(systemName: "checkmark.circle.fill")
                            .font(.system(size: 22))
                            .foregroundStyle(.white, WTheme.positive)
                            .transition(.scale(scale: 0.4).combined(with: .opacity))
                    }
                }
                GeometryReader { proxy in
                    ZStack(alignment: .leading) {
                        Capsule().fill(WTheme.fill)
                        Capsule()
                            .fill(running ? WTheme.ink : WTheme.positive)
                            .frame(width: max(8, proxy.size.width * CGFloat(finished) / CGFloat(max(total, 1))))
                    }
                }
                .frame(height: 6)

                if let current {
                    VStack(spacing: 8) {
                        outcomeRow("Connected", current.count("connected") + current.count("updated"), .emerald)
                        outcomeRow("Waiting for a sign-in", current.count("needs_signin"), .sky)
                        outcomeRow("Failed", current.count("failed"), .rose)
                        outcomeRow("Skipped", current.count("skipped") + current.count("cancelled"), .slate)
                    }
                }
            }
            .padding(16)
            .connectCard()
            .animation(.spring(response: 0.5, dampingFraction: 0.85), value: finished)
            .animation(.spring(response: 0.5, dampingFraction: 0.8), value: running)
            .staggerIn(0)

            if let causes = current?.causes, !causes.isEmpty {
                VStack(alignment: .leading, spacing: 12) {
                    ForEach(causes, id: \.self) { cause in
                        VStack(alignment: .leading, spacing: 3) {
                            Text("\(cause.title) (\(cause.count))")
                                .font(.system(size: 14, weight: .semibold))
                                .foregroundStyle(WTheme.ink)
                            Text(cause.fix)
                                .font(.system(size: 13))
                                .foregroundStyle(WTheme.inkSecondary)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
                .padding(16)
                .frame(maxWidth: .infinity, alignment: .leading)
                .connectCard()
                .padding(.top, 12)
                .transition(.opacity.combined(with: .move(edge: .top)))
            }
        }
    }

    @ViewBuilder
    private func outcomeRow(_ label: String, _ count: Int, _ tone: Tone) -> some View {
        if count > 0 || label == "Connected" {
            HStack(spacing: 8) {
                Circle().fill(tone.color).frame(width: 7, height: 7)
                Text(label)
                    .font(.system(size: 14))
                    .foregroundStyle(WTheme.inkSecondary)
                Spacer()
                Text("\(count)")
                    .font(.system(size: 14, weight: .semibold))
                    .monospacedDigit()
                    .foregroundStyle(WTheme.ink)
                    .contentTransition(.numericText())
            }
        }
    }

    // MARK: - Done (one mailbox)

    private var donePage: some View {
        VStack(alignment: .leading, spacing: 0) {
            header("done", "Mailbox connected", "Syncing has started. It's in your mailbox list already.")

            if let account = connectedAccount {
                HStack(spacing: 12) {
                    MailboxAvatar(email: account.email, avatarURL: account.avatarURL, brand: account.brand, seed: account.id, size: 42, ring: WTheme.raised)
                    VStack(alignment: .leading, spacing: 3) {
                        Text(account.email)
                            .font(.system(size: 16, weight: .medium))
                            .foregroundStyle(WTheme.ink)
                            .lineLimit(1)
                            .truncationMode(.middle)
                        Text(account.providerLabel)
                            .font(.system(size: 13))
                            .foregroundStyle(WTheme.inkSecondary)
                    }
                    Spacer(minLength: 8)
                    Image(systemName: "checkmark.circle.fill")
                        .font(.system(size: 20))
                        .foregroundStyle(.white, WTheme.positive)
                }
                .padding(14)
                .frame(maxWidth: .infinity, alignment: .leading)
                .connectCard()
                .padding(.bottom, 12)
                .staggerIn(0)
            }

            warmupBlock
                .staggerIn(1)
        }
    }

    private var warmupBlock: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 12) {
                IconTile(symbol: "flame.fill", tone: .orange, size: 42)
                VStack(alignment: .leading, spacing: 4) {
                    Text(warmupOffer == .active ? "Warming up" : "Start warming up")
                        .font(.system(size: 15.5, weight: .medium))
                        .foregroundStyle(WTheme.ink)
                        .contentTransition(.opacity)
                    if warmupOffer == .active {
                        StatusPill(text: "warming", tone: .orange, pulsing: true)
                            .transition(.scale(scale: 0.6).combined(with: .opacity))
                    }
                }
                Spacer(minLength: 8)
                if warmupOffer == .active {
                    Image(systemName: "checkmark.circle.fill")
                        .font(.system(size: 20))
                        .foregroundStyle(.white, Tone.orange.color)
                        .transition(.scale(scale: 0.4).combined(with: .opacity))
                }
            }

            Text(warmupOffer == .active
                ? "This mailbox is building sender reputation. Track its progress from the mailbox list."
                : "Builds sender reputation before campaigns. Starts at 10 a day and ramps automatically.")
                .font(.system(size: 13.5))
                .foregroundStyle(WTheme.inkSecondary)
                .fixedSize(horizontal: false, vertical: true)

            if case let .failed(message) = warmupOffer {
                Text(message)
                    .font(.system(size: 13))
                    .foregroundStyle(WTheme.negative)
                    .fixedSize(horizontal: false, vertical: true)
                    .transition(.opacity)
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .connectCard()
        .animation(.spring(response: 0.4, dampingFraction: 0.8), value: warmupOffer)
    }

    // MARK: - Field primitives

    private func fieldBlock(_ label: String, helper: String? = nil, @ViewBuilder content: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: 7) {
            Text(label)
                .font(.system(size: 13, weight: .medium))
                .foregroundStyle(WTheme.inkSecondary)
            content()
            if let helper {
                Text(helper)
                    .font(.system(size: 12.5))
                    .foregroundStyle(WTheme.inkTertiary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    private func styledField(
        _ placeholder: String,
        text: Binding<String>,
        field: Field,
        secure: Bool = false,
        submit: SubmitLabel = .next,
        onSubmit: @escaping () -> Void = {}
    ) -> some View {
        Group {
            if secure {
                SecureField(placeholder, text: text)
            } else {
                TextField(placeholder, text: text)
            }
        }
        .focused($focused, equals: field)
        .submitLabel(submit)
        .onSubmit(onSubmit)
        .font(.system(size: 16))
        .foregroundStyle(WTheme.ink)
        .padding(.horizontal, 16)
        .frame(height: 52)
        .background(WTheme.raised, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 14, style: .continuous)
                .strokeBorder(focused == field ? WTheme.ink : WTheme.hairlineStrong, lineWidth: focused == field ? 1.5 : 1)
        )
        .animation(.easeOut(duration: 0.18), value: focused)
    }

    // MARK: - Flow

    private var trimmedName: String { name.trimmingCharacters(in: .whitespaces) }
    private var trimmedEmail: String { email.trimmingCharacters(in: .whitespaces) }
    private var emailLooksValid: Bool { trimmedEmail.contains("@") && trimmedEmail.contains(".") }

    private var canAdvance: Bool {
        switch page {
        case .provider, .google, .grants, .vendors:
            return false
        case .twoStep, .createPassword, .running, .done:
            return true
        case .appConnect:
            return !trimmedName.isEmpty && emailLooksValid && !cleanedAppPassword.isEmpty
        case .account:
            return trimmedName.count >= 2 && emailLooksValid && knownHost?.refusal == nil
        case .imap:
            return !imapHost.trimmingCharacters(in: .whitespaces).isEmpty
                && (Int(imapPort) ?? 0) > 0
                && !imapPassword.isEmpty
        case .smtp:
            let port = Int(smtpPort) ?? 0
            return !smtpHost.trimmingCharacters(in: .whitespaces).isEmpty
                && (port == 465 || port == 587)
                && (sameLogin || (!smtpUsername.isEmpty && !smtpPassword.isEmpty))
        case .users, .vendorMailboxes:
            return !picked.isEmpty
        case .vendorKey:
            return (vendorEntry?.fields ?? []).allSatisfy {
                !$0.required || !(vendorValues[$0.key] ?? "").trimmingCharacters(in: .whitespaces).isEmpty
            }
        case .fileSource:
            return file != nil || !pasted.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        case .fileReview:
            guard let preview else { return false }
            return importCount > 0 && (preview.mapsPassword || !sharedPassword.isEmpty || preview.summary.ready > 0)
        }
    }

    /// Focuses a field once the step has slid in, so the keyboard doesn't fight the push.
    private func focusSoon(_ field: Field) {
        Task {
            try? await Task.sleep(for: .milliseconds(450))
            focused = field
        }
    }

    /// Moves forward onto `target` under `route`.
    private func navigate(to target: Page, route next: Route) {
        focused = nil
        direction = 1
        withAnimation(Self.push) {
            route = next
            page = target
            scroll.scrollTo(edge: .top)
        }
    }

    private func goTo(_ target: Page) {
        focused = nil
        let path = pagePath
        let from = path.firstIndex(of: page) ?? 0
        let to = path.firstIndex(of: target) ?? 0
        direction = to >= from ? 1 : -1
        withAnimation(Self.push) {
            page = target
            scroll.scrollTo(edge: .top)
        }
    }

    private func goBack() {
        errorMessage = nil
        let path = pagePath
        guard let index = path.firstIndex(of: page), index > 0 else { return }
        let previous = path[index - 1]
        focused = nil
        direction = -1
        withAnimation(Self.push) {
            page = previous
            scroll.scrollTo(edge: .top)
            if previous == .provider || previous == .google { route = .undecided }
            if previous == .vendors { vendorViaKey = false }
        }
    }

    private func primaryAction() async {
        guard canAdvance, !primaryBusy else { return }
        errorMessage = nil
        switch page {
        case .provider, .google, .grants, .vendors:
            break
        case .twoStep:
            goTo(.createPassword)
        case .createPassword:
            goTo(.appConnect)
        case .appConnect:
            await submitAppPassword()
        case .account:
            fillKnownHost()
            goTo(.imap)
        case .imap:
            goTo(.smtp)
        case .smtp:
            await submitSMTP()
        case .users:
            await connectPicked()
        case .vendorKey:
            await saveVendorKey()
        case .vendorMailboxes:
            await importVendorMailboxes()
        case .fileSource:
            await runPreview()
        case .fileReview:
            await startFileImport()
        case .running:
            onClose()
        case .done:
            switch warmupOffer {
            case .offer: await startWarmup()
            case .active, .failed: onClose()
            case .starting: break
            }
        }
    }

    private func showError(_ error: Error) {
        showError((error as? APIError)?.errorDescription ?? error.localizedDescription)
    }

    private func showError(_ message: String) {
        withAnimation(.easeOut(duration: 0.2)) { errorMessage = message }
        errorPulse += 1
    }

    private func finishConnect(_ account: EmailAccount) {
        connectedAccount = account
        onConnected?(account)
        UINotificationFeedbackGenerator().notificationOccurred(.success)
        goTo(.done)
    }

    private func startedImport(_ started: MailboxImportJob) {
        job = started
        onImported?()
        UINotificationFeedbackGenerator().notificationOccurred(.success)
        goTo(.running)
    }

    /// Fills the servers in for a known host, and again when the domain changes.
    private func fillKnownHost() {
        let domain = trimmedEmail.split(separator: "@").last.map(String.init)?.lowercased()
        guard let host = knownHost, host.refusal == nil, filledFor != domain else { return }
        imapHost = host.imapHost
        imapPort = "993"
        smtpHost = host.smtpHost
        smtpPort = String(host.smtpPort)
        imapUsername = trimmedEmail
        filledFor = domain
    }

    static func security(forPort port: Int) -> String {
        port == 465 || port == 993 ? "tls" : "starttls"
    }

    // MARK: - Network

    private struct DataList<T: Decodable>: Decodable { var data: [T] }

    private func loadSources() async {
        async let config: MailboxGrantConfig? = try? env.api.get("emails/grants/config")
        async let grantList: DataList<MailboxGrant>? = try? env.api.get("emails/grants")
        async let catalogList: DataList<VendorCatalogEntry>? = try? env.api.get("emails/vendors/catalog")
        async let connections: DataList<VendorConnection>? = try? env.api.get("emails/vendors")
        async let cloud: DataList<CloudWorkspaceMailbox>? = try? env.api.get("cloud-link/workspace-mailboxes")
        let loaded = await (config, grantList, catalogList, connections, cloud)
        withAnimation(.easeOut(duration: 0.25)) {
            grantConfig = loaded.0
            grants = loaded.1?.data ?? []
            if let entries = loaded.2?.data, !entries.isEmpty { catalog = entries }
            vendorConnections = loaded.3?.data ?? []
            cloudMailboxes = loaded.4?.data ?? []
            sourcesLoaded = true
        }
    }

    private func adopt(_ box: CloudWorkspaceMailbox) async {
        adopting = box.id
        defer { adopting = nil }
        do {
            let account: EmailAccount = try await env.api.post("cloud-link/workspace-mailboxes/\(box.id)/adopt")
            cloudMailboxes.removeAll { $0.id == box.id }
            route = .cloud
            finishConnect(account)
        } catch {
            showError(error)
        }
    }

    private func checkGrant(_ item: MailboxGrant) async {
        checkingGrant = item.id
        defer { checkingGrant = nil }
        do {
            let fresh: MailboxGrant = try await env.api.post("emails/grants/\(item.id)/check")
            withAnimation(.snappy) {
                grants = (grants ?? []).map { $0.id == fresh.id ? fresh : $0 }
            }
        } catch {
            showError(error)
        }
    }

    private func resetPick() {
        picked = []
        query = ""
        listError = nil
    }

    private func openGrant(_ item: MailboxGrant) {
        guard item.isActive else { return }
        if grant?.id != item.id {
            users = nil
            resetPick()
        }
        grant = item
        goTo(.users)
        if users == nil { Task { await loadUsers() } }
    }

    private func loadUsers() async {
        guard let grant else { return }
        listError = nil
        do {
            let list: DataList<GrantDirectoryUser> = try await env.api.get("emails/grants/\(grant.id)/users")
            withAnimation(.easeOut(duration: 0.25)) { users = list.data }
        } catch {
            listError = (error as? APIError)?.errorDescription ?? error.localizedDescription
        }
    }

    private func connectPicked() async {
        guard let grant, !picked.isEmpty, !busy else { return }
        busy = true
        defer { busy = false }
        let body = GrantConnectBody(userIDs: Array(picked), options: .init(settings: .init(warmup: warmupImported)))
        do {
            let started: MailboxImportJob = try await env.api.post("emails/grants/\(grant.id)/connect", body: body, idempotent: true)
            startedImport(started)
        } catch let error as APIError where error.code == "reauth_required" {
            showReauth = true
        } catch {
            showError(error)
        }
    }

    private func openVendorKey(_ entry: VendorCatalogEntry, updating: VendorConnection?) {
        vendorEntry = entry
        vendorConnection = updating
        vendorLabel = ""
        vendorValues = [:]
        vendorViaKey = true
        goTo(.vendorKey)
        if let first = entry.fields.first(where: \.required) { focusSoon(.vendorField(first.key)) }
    }

    private func openVendorConnection(_ conn: VendorConnection) {
        if vendorConnection?.id != conn.id {
            vendorMailboxes = nil
            resetPick()
        }
        vendorConnection = conn
        vendorEntry = (catalog ?? []).first { $0.id == conn.vendor }
        vendorViaKey = false
        goTo(.vendorMailboxes)
        if vendorMailboxes == nil { Task { await loadVendorMailboxes() } }
    }

    private func saveVendorKey() async {
        guard let entry = vendorEntry, !busy else { return }
        focused = nil
        busy = true
        defer { busy = false }
        var fields: [String: String] = [:]
        for field in entry.fields {
            let value = (vendorValues[field.key] ?? "").trimmingCharacters(in: .whitespaces)
            if !value.isEmpty { fields[field.key] = value }
        }
        do {
            let conn: VendorConnection
            if let existing = vendorConnection {
                conn = try await env.api.patch("emails/vendors/\(existing.id)", body: UpdateVendorBody(fields: fields))
            } else {
                let label = vendorLabel.trimmingCharacters(in: .whitespaces)
                conn = try await env.api.post("emails/vendors", body: CreateVendorBody(vendor: entry.id, label: label, fields: fields))
            }
            vendorConnections = [conn] + vendorConnections.filter { $0.id != conn.id }
            vendorConnection = conn
            vendorMailboxes = nil
            resetPick()
            goTo(.vendorMailboxes)
            await loadVendorMailboxes()
        } catch let error as APIError where error.code == "mailbox_vendor_rate_limited" {
            showError("\(entry.label) is limiting requests right now. Wait a minute and try again.")
        } catch {
            showError(error)
        }
    }

    private func loadVendorMailboxes() async {
        guard let conn = vendorConnection else { return }
        listError = nil
        do {
            let list: DataList<VendorMailbox> = try await env.api.get("emails/vendors/\(conn.id)/mailboxes")
            withAnimation(.easeOut(duration: 0.25)) { vendorMailboxes = list.data }
        } catch {
            listError = (error as? APIError)?.errorDescription ?? error.localizedDescription
        }
    }

    private func importVendorMailboxes() async {
        guard let conn = vendorConnection, !picked.isEmpty, !busy else { return }
        busy = true
        defer { busy = false }
        let body = VendorImportBody(mailboxIDs: Array(picked), options: .init(settings: .init(warmup: warmupImported)))
        do {
            let started: MailboxImportJob = try await env.api.post("emails/vendors/\(conn.id)/import", body: body, idempotent: true)
            startedImport(started)
        } catch {
            showError(error)
        }
    }

    private func readFile(_ result: Result<URL, Error>) {
        guard case let .success(url) = result else { return }
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        guard let data = try? Data(contentsOf: url) else {
            showError("That file couldn't be read.")
            return
        }
        let ext = url.pathExtension.lowercased()
        let mime = ext == "xlsx"
            ? "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
            : (ext == "csv" ? "text/csv" : "text/plain")
        withAnimation(.snappy) {
            file = APIClient.UploadFile(field: "file", filename: url.lastPathComponent, mimeType: mime, data: data)
            errorMessage = nil
        }
    }

    private func importFields(_ options: FileImportOptions, mapping: [String: String]?) -> [String: String] {
        var fields: [String: String] = [:]
        if file == nil { fields["text"] = pasted }
        if let mapping, !mapping.isEmpty, let json = try? JSONEncoder().encode(mapping) {
            fields["mapping"] = String(decoding: json, as: UTF8.self)
        }
        if let json = try? JSONEncoder().encode(options) {
            fields["options"] = String(decoding: json, as: UTF8.self)
        }
        return fields
    }

    private func runPreview() async {
        focused = nil
        busy = true
        defer { busy = false }
        do {
            let result: MailboxImportPreview = try await env.api.postMultipart(
                "emails/imports/preview", fields: importFields(FileImportOptions(), mapping: nil), file: file
            )
            preview = result
            sharedPassword = ""
            goTo(.fileReview)
        } catch {
            showError(error)
        }
    }

    /// Re-reads the list with the shared password so the counts are honest.
    private func refreshPreviewForSharedPassword() async {
        guard page == .fileReview, let current = preview, !current.mapsPassword, !sharedPassword.isEmpty else { return }
        try? await Task.sleep(for: .milliseconds(600))
        guard !Task.isCancelled else { return }
        let options = FileImportOptions(hasHeader: current.hasHeader, sharedPassword: sharedPassword)
        if let fresh: MailboxImportPreview = try? await env.api.postMultipart(
            "emails/imports/preview", fields: importFields(options, mapping: current.mapping), file: file
        ) {
            withAnimation(.snappy) { preview = fresh }
        }
    }

    private func startFileImport() async {
        guard let current = preview, !busy else { return }
        focused = nil
        busy = true
        defer { busy = false }
        let options = FileImportOptions(
            hasHeader: current.hasHeader,
            sharedPassword: sharedPassword.isEmpty ? nil : sharedPassword,
            onExisting: "update",
            saveMapping: true,
            settings: .init(warmup: warmupImported)
        )
        do {
            let started: MailboxImportJob = try await env.api.postMultipart(
                "emails/imports", fields: importFields(options, mapping: current.mapping), file: file
            )
            startedImport(started)
        } catch {
            showError(error)
        }
    }

    private func pollJob() async {
        guard let id = job?.id, id != "debug" else { return }
        while !Task.isCancelled, job?.isRunning == true {
            try? await Task.sleep(for: .seconds(2.5))
            guard !Task.isCancelled else { return }
            if let fresh: MailboxImportJob = try? await env.api.get("emails/imports/\(id)") {
                withAnimation(.snappy) { job = fresh }
                if !fresh.isRunning {
                    onImported?()
                    UINotificationFeedbackGenerator().notificationOccurred(.success)
                }
            }
        }
    }

    private func submitAppPassword() async {
        focused = nil
        busy = true
        defer { busy = false }
        let address = trimmedEmail
        let gmail = KnownHost.gmail
        let body = MailboxSMTPConnectBody(
            email: address,
            name: trimmedName,
            smtp: MailboxServerCredentials(username: address, password: cleanedAppPassword, host: gmail.smtpHost, port: gmail.smtpPort, security: "starttls"),
            imap: MailboxServerCredentials(username: address, password: cleanedAppPassword, host: gmail.imapHost, port: 993, security: "tls")
        )
        do {
            let account: EmailAccount = try await env.api.post("emails/onboarding/smtp-imap", body: body)
            finishConnect(account)
        } catch {
            showError(error)
        }
    }

    private func submitSMTP() async {
        focused = nil
        busy = true
        defer { busy = false }
        let imapPortValue = Int(imapPort) ?? 993
        let smtpPortValue = Int(smtpPort) ?? 587
        let body = MailboxSMTPConnectBody(
            email: trimmedEmail,
            name: trimmedName,
            smtp: MailboxServerCredentials(
                username: sameLogin ? imapUsername : smtpUsername,
                password: sameLogin ? imapPassword : smtpPassword,
                host: smtpHost.trimmingCharacters(in: .whitespaces),
                port: smtpPortValue,
                security: Self.security(forPort: smtpPortValue)
            ),
            imap: MailboxServerCredentials(
                username: imapUsername,
                password: imapPassword,
                host: imapHost.trimmingCharacters(in: .whitespaces),
                port: imapPortValue,
                security: Self.security(forPort: imapPortValue)
            )
        )
        do {
            let account: EmailAccount = try await env.api.post("emails/onboarding/smtp-imap", body: body)
            finishConnect(account)
        } catch {
            showError(error)
        }
    }

    private func startWarmup() async {
        guard let account = connectedAccount, warmupOffer == .offer else { return }
        withAnimation(.easeOut(duration: 0.2)) { warmupOffer = .starting }
        do {
            let updated: EmailAccount = try await env.api.post("emails/\(account.id)/warmup/start")
            connectedAccount = updated
            UINotificationFeedbackGenerator().notificationOccurred(.success)
            withAnimation(.spring(response: 0.4, dampingFraction: 0.8)) { warmupOffer = .active }
        } catch {
            let message = (error as? APIError)?.errorDescription ?? error.localizedDescription
            withAnimation(.easeOut(duration: 0.2)) { warmupOffer = .failed(message) }
            errorPulse += 1
        }
    }

    // MARK: - Debug

    #if DEBUG
    /// WARMBLY_DEBUG_CONNECT opens a step, with sample data where it needs a server.
    private func applyDebugLaunch() -> Bool {
        guard let target = ProcessInfo.processInfo.environment["WARMBLY_DEBUG_CONNECT"], target != "provider" else { return false }
        name = "Alex Rivera"
        email = "alex@sunriselabs.io"
        let sample = MailboxGrant(id: "debug", provider: .google, tenant: "sunriselabs.io", adminEmail: "it@sunriselabs.io", domains: ["sunriselabs.io"], status: "active", lastError: nil, mailboxes: 3)
        let apiKey = VendorField(key: "api_key", label: "API key", secret: true, required: true, help: "Settings, then API, in your vendor's dashboard.")
        let vendors = [
            ("inboxkit", "InboxKit"), ("zapmail", "Zapmail"), ("mailforge", "Mailforge"),
            ("infraforge", "Infraforge"), ("maildoso", "Maildoso"), ("scaledmail", "ScaledMail"),
        ].map { VendorCatalogEntry(id: $0.0, label: $0.1, website: nil, keyHelpURL: "https://example.com", fields: [apiKey]) }
        let zapmail = VendorConnection(id: "c1", vendor: "zapmail", label: "Zapmail", status: "active", lastError: nil, mailboxes: 12)
        grantConfig = MailboxGrantConfig(googleEnabled: true, microsoftEnabled: true, googleMissing: nil, microsoftMissing: nil)
        grants = [sample]
        catalog = vendors
        sourcesLoaded = true
        switch target {
        case "home": break
        case "google": page = .google
        case "twostep": route = .appPassword; page = .twoStep
        case "apppassword": route = .appPassword; page = .createPassword
        case "appconnect": route = .appPassword; page = .appConnect; email = "alex@gmail.com"
        case "account": route = .other; email = "alex@zoho.com"; page = .account
        case "imap": route = .other; email = "alex@zoho.com"; fillKnownHost(); page = .imap
        case "smtp": route = .other; email = "alex@zoho.com"; fillKnownHost(); page = .smtp
        case "grants": route = .grant; page = .grants
        case "vendors":
            vendorConnections = [zapmail]
            route = .vendor; page = .vendors
        case "vendorkey":
            vendorEntry = vendors[0]; vendorViaKey = true; route = .vendor; page = .vendorKey
        case "vendormailboxes":
            vendorEntry = vendors[1]
            vendorConnection = zapmail
            vendorMailboxes = [
                VendorMailbox(id: "1", email: "alex@outreach-acme.io", name: "Alex Rivera", provider: "google", status: "active", workspace: nil, connected: true),
                VendorMailbox(id: "2", email: "maya@outreach-acme.io", name: "Maya Chen", provider: "google", status: "active", workspace: nil, connected: false),
                VendorMailbox(id: "3", email: "sam@getacme.co", name: "Sam Okafor", provider: "microsoft", status: "active", workspace: nil, connected: false),
                VendorMailbox(id: "4", email: "lena@getacme.co", name: "Lena Fischer", provider: "google", status: "suspended", workspace: nil, connected: false),
            ]
            picked = ["2"]
            route = .vendor; page = .vendorMailboxes
        case "file": route = .file; page = .fileSource
        case "filereview":
            route = .file
            pasted = "alex@acme.io, pass"
            preview = MailboxImportPreview(
                format: "csv", hasHeader: true, vendor: .init(id: "zapmail", label: "Zapmail"),
                mapping: ["0": "email", "1": "app_password"],
                summary: .init(total: 14, ready: 11, needsSignin: 2, invalid: 1, existing: 0, duplicate: 0),
                domains: [
                    .init(domain: "outreach-acme.io", mailHost: "google_workspace", label: "Google Workspace", rows: 9, passwordAuth: "app_password", appPasswordURL: "https://myaccount.google.com/apppasswords"),
                    .init(domain: "getacme.co", mailHost: "microsoft365", label: "Microsoft 365", rows: 2, passwordAuth: "oauth_only", appPasswordURL: nil),
                    .init(domain: "acme-mail.com", mailHost: "zoho", label: "Zoho Mail", rows: 3, passwordAuth: "app_password", appPasswordURL: "https://accounts.zoho.com"),
                ],
                issues: [.init(cause: "bad_email", title: "Not an email address", fix: "Row 7 has no valid address in the email column.", count: 1)]
            )
            page = .fileReview
        case "users", "running":
            route = .grant
            grant = sample
            users = [
                GrantDirectoryUser(id: "1", email: "alex@sunriselabs.io", name: "Alex Rivera", enabled: true, connected: true, upgrade: nil),
                GrantDirectoryUser(id: "2", email: "maya@sunriselabs.io", name: "Maya Chen", enabled: true, connected: false, upgrade: nil),
                GrantDirectoryUser(id: "3", email: "sam@sunriselabs.io", name: "Sam Okafor", enabled: true, connected: false, upgrade: true),
                GrantDirectoryUser(id: "4", email: "lena@sunriselabs.io", name: "Lena Fischer", enabled: true, connected: false, upgrade: nil),
                GrantDirectoryUser(id: "5", email: "old@sunriselabs.io", name: "Former Intern", enabled: false, connected: false, upgrade: nil),
            ]
            picked = ["2", "4"]
            if target == "running" {
                job = MailboxImportJob(id: "debug", status: "running", total: 2, counts: ["connected": 1], causes: nil)
                page = .running
            } else {
                page = .users
            }
        default: return false
        }
        return true
    }
    #endif
}

/// Rows and cards rise in one after another when a step opens.
private struct StaggerIn: ViewModifier {
    let index: Int
    @State private var shown = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    func body(content: Content) -> some View {
        content
            .opacity(shown ? 1 : 0)
            .offset(y: shown || reduceMotion ? 0 : 14)
            .onAppear {
                withAnimation(.spring(response: 0.5, dampingFraction: 0.84).delay(0.06 + Double(min(index, 8)) * 0.045)) {
                    shown = true
                }
            }
    }
}

private extension View {
    func staggerIn(_ index: Int) -> some View { modifier(StaggerIn(index: index)) }
}

/// A square that ticks when its row is picked.
private struct PickSquare: View {
    let on: Bool

    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: 7, style: .continuous)
                .strokeBorder(WTheme.hairlineStrong, lineWidth: 1.5)
                .opacity(on ? 0 : 1)
            RoundedRectangle(cornerRadius: 7, style: .continuous)
                .fill(WTheme.ink)
                .scaleEffect(on ? 1 : 0.4)
                .opacity(on ? 1 : 0)
            Image(systemName: "checkmark")
                .font(.system(size: 11.5, weight: .bold))
                .foregroundStyle(WTheme.inkInverse)
                .scaleEffect(on ? 1 : 0.3)
                .opacity(on ? 1 : 0)
        }
        .frame(width: 22, height: 22)
        .animation(.spring(response: 0.3, dampingFraction: 0.6), value: on)
    }
}

/// The well-known hosts whose servers the app fills in from the address.
/// Mirrors the backend's `mailhost` table for consumer domains.
private struct KnownHost: Equatable {
    var label: String
    var mark: MailboxBrand.Mark
    var smtpHost: String
    var smtpPort: Int
    var imapHost: String
    var appPasswordURL: String?
    /// Why this flow cannot connect the address, when it can't.
    var refusal: String?

    static let gmail = KnownHost(
        label: "Gmail", mark: .google, smtpHost: "smtp.gmail.com", smtpPort: 587, imapHost: "imap.gmail.com",
        appPasswordURL: "https://myaccount.google.com/apppasswords"
    )

    static func forEmail(_ email: String) -> KnownHost? {
        guard let at = email.lastIndex(of: "@") else { return nil }
        let domain = email[email.index(after: at)...].lowercased()
        guard domain.contains(".") else { return nil }
        let first = domain.split(separator: ".").first.map(String.init) ?? ""
        let tld = domain.split(separator: ".").last.map(String.init) ?? "com"

        switch domain {
        case "gmail.com", "googlemail.com":
            return gmail
        case "aol.com":
            return KnownHost(label: "AOL Mail", mark: .asset("Host-aol"), smtpHost: "smtp.aol.com", smtpPort: 465, imapHost: "imap.aol.com", appPasswordURL: "https://login.aol.com/account/security")
        case "icloud.com", "me.com", "mac.com":
            return KnownHost(label: "iCloud Mail", mark: .asset("Host-icloud"), smtpHost: "smtp.mail.me.com", smtpPort: 587, imapHost: "imap.mail.me.com", appPasswordURL: "https://account.apple.com/account/manage")
        case "fastmail.com", "fastmail.fm":
            return KnownHost(label: "Fastmail", mark: .asset("Host-fastmail"), smtpHost: "smtp.fastmail.com", smtpPort: 465, imapHost: "imap.fastmail.com", appPasswordURL: "https://app.fastmail.com/settings/security/apps")
        case "proton.me", "protonmail.com", "pm.me":
            return KnownHost(label: "Proton Mail", mark: .asset("Host-proton"), smtpHost: "", smtpPort: 0, imapHost: "",
                             refusal: "Proton Mail only works through Proton Bridge on a computer, so it can't connect here.")
        default:
            break
        }
        switch first {
        case "yahoo", "ymail", "rocketmail":
            return KnownHost(label: "Yahoo Mail", mark: .asset("Host-yahoo"), smtpHost: "smtp.mail.yahoo.com", smtpPort: 465, imapHost: "imap.mail.yahoo.com", appPasswordURL: "https://login.yahoo.com/account/security")
        case "zoho", "zohomail":
            let zone = "zoho." + domain.split(separator: ".").dropFirst().joined(separator: ".")
            return KnownHost(label: "Zoho Mail", mark: .asset("Host-zoho"), smtpHost: "smtp.\(zone)", smtpPort: 465, imapHost: "imap.\(zone)", appPasswordURL: "https://accounts.\(zone)/home#security/app_password")
        case "yandex":
            return KnownHost(label: "Yandex Mail", mark: .asset("Host-yandex"), smtpHost: "smtp.yandex.com", smtpPort: 465, imapHost: "imap.yandex.com", appPasswordURL: "https://id.yandex.com/security/app-passwords")
        case "gmx":
            return KnownHost(label: "GMX", mark: .asset("Host-gmx"), smtpHost: "mail.gmx.\(tld)", smtpPort: 587, imapHost: "imap.gmx.\(tld)")
        case "outlook", "hotmail", "live", "msn":
            return KnownHost(label: "Outlook.com", mark: .microsoft, smtpHost: "", smtpPort: 0, imapHost: "",
                             refusal: "Outlook.com only signs in with Microsoft, which this app doesn't offer. Connect it from the dashboard.")
        default:
            return nil
        }
    }
}

private extension View {
    /// Flat hairline card used across the connect flow.
    func connectCard(radius: CGFloat = 18) -> some View {
        background(WTheme.raised, in: RoundedRectangle(cornerRadius: radius, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: radius, style: .continuous)
                    .strokeBorder(WTheme.hairlineStrong, lineWidth: 1)
            )
    }
}

/// Confirms the account holder before a change that needs a recent sign-in,
/// then hands control back so the caller retries.
struct ReauthSheet: View {
    var onConfirmed: () -> Void

    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var secret = ""
    @State private var useCode = false
    @State private var busy = false
    @State private var errorMessage: String?
    @FocusState private var focused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 5) {
                    Text("Confirm it's you")
                        .displayFont(26)
                        .foregroundStyle(WTheme.ink)
                    Text("Connecting a whole domain's mailboxes needs a recent sign-in.")
                        .font(.system(size: 14.5))
                        .foregroundStyle(WTheme.inkSecondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer(minLength: 12)
                Button { dismiss() } label: {
                    Image(systemName: "xmark")
                        .font(.system(size: 13, weight: .bold))
                        .foregroundStyle(WTheme.inkSecondary)
                        .frame(width: 32, height: 32)
                        .background(WTheme.fill, in: Circle())
                }
                .buttonStyle(TapScaleStyle())
                .accessibilityLabel("Close")
            }
            .padding(.bottom, 22)

            Group {
                if useCode {
                    TextField("6-digit code", text: $secret)
                        .keyboardType(.numberPad)
                        .textContentType(.oneTimeCode)
                } else {
                    SecureField("Password", text: $secret)
                        .textContentType(.password)
                }
            }
            .focused($focused)
            .submitLabel(.done)
            .onSubmit { Task { await confirm() } }
            .font(.system(size: 16))
            .padding(.horizontal, 16)
            .frame(height: 52)
            .background(WTheme.raised, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: 14, style: .continuous)
                    .strokeBorder(focused ? WTheme.ink : WTheme.hairlineStrong, lineWidth: focused ? 1.5 : 1)
            )

            Button(useCode ? "Use my password instead" : "Use a two-factor code instead") {
                withAnimation(.snappy) {
                    useCode.toggle()
                    secret = ""
                    errorMessage = nil
                }
            }
            .font(.system(size: 13.5, weight: .medium))
            .foregroundStyle(WTheme.accent)
            .padding(.top, 12)

            if let errorMessage {
                Text(errorMessage)
                    .font(.system(size: 13.5))
                    .foregroundStyle(WTheme.negative)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.top, 12)
                    .transition(.opacity)
            }

            Spacer(minLength: 20)

            Button {
                Task { await confirm() }
            } label: {
                if busy {
                    ProgressView().tint(WTheme.inkInverse)
                } else {
                    Text("Confirm and connect")
                }
            }
            .buttonStyle(InkButtonStyle(size: .large, fullWidth: true))
            .disabled(secret.isEmpty)
            .allowsHitTesting(!busy)
        }
        .padding(24)
        .background(WTheme.canvas)
        .presentationDetents([.medium])
        .presentationDragIndicator(.visible)
        .sensoryFeedback(.error, trigger: errorMessage)
        .onAppear { focused = true }
    }

    private func confirm() async {
        guard !secret.isEmpty, !busy else { return }
        busy = true
        defer { busy = false }
        let body = useCode ? ReauthBody(code: secret.filter(\.isNumber)) : ReauthBody(password: secret)
        do {
            let _: EmptyBody = try await env.api.post("auth/reauth", body: body)
            dismiss()
            onConfirmed()
        } catch {
            withAnimation(.easeOut(duration: 0.2)) {
                errorMessage = (error as? APIError)?.errorDescription ?? error.localizedDescription
            }
        }
    }
}
