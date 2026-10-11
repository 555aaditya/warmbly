import SwiftUI

/// Where the app signs in: Warmbly Cloud unless someone picks their own
/// server. The app proves itself with App Attest rather than a captcha; only a
/// dev backend takes a bypass token, for the simulator, which cannot attest.
struct ServerConfigSheet: View {
    @Environment(\.dismiss) private var dismiss

    enum Choice: Hashable { case cloud, selfHosted, local }

    @State private var choice: Choice
    @State private var customOrigin: String
    @State private var turnstile = AppConfig.turnstileToken
    @State private var realtime = AppConfig.realtimeOriginOverride
    @State private var showAdvanced = false
    @State private var probe: Probe = .idle
    @State private var saved = 0

    @FocusState private var focused: Field?
    private enum Field { case origin, turnstile, realtime }

    enum Probe: Equatable { case idle, checking, reachable(ServerInfo?), unreachable }

    /// The slice of `GET /v1/auth/config` the sheet shows.
    struct ServerInfo: Decodable, Equatable {
        let captcha: Bool
        let appAttest: Bool?
        let selfHosted: Bool?
        let brand: Brand?
        struct Brand: Decodable, Equatable { let name: String? }
    }

    /// False only once the chosen server said it verifies no captcha.
    private var needsCaptcha: Bool {
        if case .reachable(let info?) = probe { return info.captcha }
        return true
    }

    /// A server that wants a captcha but takes no App Attest proof: email sign-in is web-only there.
    private var emailSignInOff: Bool {
        if case .reachable(let info?) = probe { return info.captcha && info.appAttest != true }
        return false
    }

    /// A dev backend's bypass token, for the simulator (which cannot attest) or a server without App Attest.
    private var asksBypassToken: Bool { (choice == .local && needsCaptcha) || (choice == .selfHosted && emailSignInOff) }

    init() {
        let origin = AppConfig.serverOrigin
        switch origin {
        case AppConfig.cloudOrigin:
            _choice = State(initialValue: .cloud)
            _customOrigin = State(initialValue: "")
        case AppConfig.localOrigin:
            _choice = State(initialValue: .local)
            _customOrigin = State(initialValue: "")
        default:
            _choice = State(initialValue: .selfHosted)
            _customOrigin = State(initialValue: origin)
        }
    }

    /// The origin the current choice resolves to, or nil while it is unusable.
    private var resolvedOrigin: String? {
        switch choice {
        case .cloud: return AppConfig.cloudOrigin
        case .local: return AppConfig.localOrigin
        case .selfHosted:
            let value = AppConfig.normalizeOrigin(customOrigin)
            guard let url = URL(string: value), url.host() != nil else { return nil }
            return value
        }
    }

    private var showsLocal: Bool {
        #if DEBUG
        return true
        #else
        return choice == .local
        #endif
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                header
                    .padding(.bottom, 22)

                options

                status
                    .padding(.top, 10)
                    .padding(.horizontal, 6)

                if choice != .cloud {
                    advanced
                        .padding(.top, 26)
                        .transition(.opacity.combined(with: .move(edge: .top)))
                }
            }
            .padding(.horizontal, 20)
            .padding(.top, 22)
            .padding(.bottom, 16)
        }
        .scrollBounceBehavior(.basedOnSize)
        .scrollDismissesKeyboard(.interactively)
        .safeAreaInset(edge: .bottom) { saveBar }
        .background(WTheme.groupedPage)
        .presentationBackground(WTheme.groupedPage)
        .presentationDetents([.medium, .large])
        .presentationDragIndicator(.visible)
        .presentationCornerRadius(30)
        .animation(.spring(response: 0.42, dampingFraction: 0.86), value: choice)
        .animation(.spring(response: 0.4, dampingFraction: 0.86), value: showAdvanced)
        .animation(.spring(response: 0.4, dampingFraction: 0.86), value: needsCaptcha)
        .sensoryFeedback(.selection, trigger: choice)
        .sensoryFeedback(.success, trigger: saved)
        .task(id: resolvedOrigin) { await check(resolvedOrigin) }
    }

    // MARK: Header

    private var header: some View {
        HStack(alignment: .top, spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                Text("Server")
                    .displayFont(28)
                    .foregroundStyle(WTheme.ink)
                Text("Where this app signs in.")
                    .font(.system(size: 15))
                    .foregroundStyle(WTheme.inkSecondary)
            }
            Spacer(minLength: 12)
            Button { dismiss() } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 13, weight: .bold))
                    .foregroundStyle(WTheme.inkSecondary)
                    .frame(width: 32, height: 32)
                    .background(WTheme.fill, in: Circle())
            }
            .buttonStyle(PressableButtonStyle())
            .accessibilityLabel("Close")
        }
    }

    // MARK: Options

    private var options: some View {
        VStack(spacing: 0) {
            optionRow(
                .cloud,
                title: "Warmbly Cloud",
                subtitle: "Hosted by Warmbly",
                badge: "Default"
            ) {
                WarmblyLogo()
                    .fill(WTheme.accent)
                    .frame(width: 17, height: 18)
                    .frame(width: 34, height: 34)
                    .background(WTheme.accent.opacity(0.13), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            }

            divider

            VStack(spacing: 0) {
                optionRow(.selfHosted, title: "Self-hosted", subtitle: "Your own Warmbly instance") {
                    SettingsIcon(symbol: "server.rack", color: WTheme.special, size: 34)
                }
                if choice == .selfHosted {
                    originField
                        .padding(.leading, 62)
                        .padding(.trailing, 14)
                        .padding(.bottom, 14)
                        .transition(.opacity.combined(with: .move(edge: .top)))
                }
            }

            if showsLocal {
                divider
                optionRow(.local, title: "Local development", subtitle: "localhost:8080 on this Mac") {
                    SettingsIcon(symbol: "laptopcomputer", color: WTheme.warning, size: 34)
                }
            }
        }
        .background(WTheme.groupedCard, in: RoundedRectangle(cornerRadius: 22, style: .continuous))
        .clipShape(RoundedRectangle(cornerRadius: 22, style: .continuous))
    }

    private var divider: some View {
        Rectangle()
            .fill(WTheme.hairlineStrong)
            .frame(height: 0.5)
            .padding(.leading, 62)
    }

    private func optionRow(
        _ value: Choice,
        title: String,
        subtitle: String,
        badge: String? = nil,
        @ViewBuilder icon: () -> some View
    ) -> some View {
        let selected = choice == value
        return Button {
            select(value)
        } label: {
            HStack(spacing: 14) {
                icon()
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 7) {
                        Text(title)
                            .font(.system(size: 16, weight: .semibold))
                            .foregroundStyle(WTheme.ink)
                        if let badge {
                            Text(badge)
                                .font(.system(size: 11, weight: .semibold))
                                .foregroundStyle(WTheme.accent)
                                .padding(.horizontal, 7)
                                .padding(.vertical, 2.5)
                                .background(WTheme.accentSoft, in: Capsule())
                        }
                    }
                    Text(subtitle)
                        .font(.system(size: 13.5))
                        .foregroundStyle(WTheme.inkSecondary)
                }
                Spacer(minLength: 8)
                RadioMark(on: selected)
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 13)
            .contentShape(Rectangle())
        }
        .buttonStyle(MoreRowPressStyle())
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    private var originField: some View {
        HStack(spacing: 8) {
            Image(systemName: "link")
                .font(.system(size: 13, weight: .medium))
                .foregroundStyle(WTheme.inkTertiary)
            TextField("warmbly.yourcompany.com", text: $customOrigin)
                .font(.system(size: 15))
                .foregroundStyle(WTheme.ink)
                .keyboardType(.URL)
                .textContentType(.URL)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .submitLabel(.done)
                .focused($focused, equals: .origin)
        }
        .padding(.horizontal, 12)
        .frame(height: 44)
        .background(WTheme.fill, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 12, style: .continuous)
                .strokeBorder(focused == .origin ? WTheme.accent : .clear, lineWidth: 1.5)
        )
        .animation(.easeOut(duration: 0.18), value: focused == .origin)
    }

    // MARK: Status

    private var status: some View {
        HStack(spacing: 7) {
            Group {
                switch probe {
                case .checking, .idle:
                    ProgressView().controlSize(.mini)
                case .reachable:
                    Circle().fill(WTheme.positive).frame(width: 7, height: 7)
                case .unreachable:
                    Circle().fill(WTheme.negative).frame(width: 7, height: 7)
                }
            }
            .frame(width: 12, height: 12)
            Text(statusText)
                .font(.system(size: 12.5))
                .foregroundStyle(WTheme.inkSecondary)
                .lineLimit(1)
                .truncationMode(.middle)
                .contentTransition(.opacity)
            if choice == .selfHosted, emailSignInOff {
                Text("Email sign-in off")
                    .font(.system(size: 11.5, weight: .semibold))
                    .foregroundStyle(WTheme.warning)
                    .padding(.horizontal, 7)
                    .padding(.vertical, 2.5)
                    .background(WTheme.warning.opacity(0.12), in: Capsule())
                    .fixedSize()
                    .transition(.opacity.combined(with: .scale(scale: 0.85)))
            }
        }
        .opacity(resolvedOrigin == nil ? 0 : 1)
        .animation(.easeOut(duration: 0.2), value: probe)
    }

    private var statusText: String {
        let host = resolvedOrigin.flatMap { URL(string: $0)?.host(percentEncoded: false) } ?? ""
        switch probe {
        case .idle, .checking: return "Checking \(host)"
        case .reachable(let info):
            let name = info?.brand?.name.flatMap { $0.isEmpty ? nil : $0 }
            return "Connected to \(choice == .selfHosted ? name ?? host : host)"
        case .unreachable: return "Can't reach \(host)"
        }
    }

    private func check(_ origin: String?) async {
        guard let origin, let url = URL(string: origin + "/v1/auth/config") else {
            probe = .idle
            return
        }
        probe = .checking
        try? await Task.sleep(for: .milliseconds(450))
        guard !Task.isCancelled else { return }
        var request = URLRequest(url: url, timeoutInterval: 5)
        request.httpMethod = "GET"
        let result: Probe
        do {
            let (data, response) = try await URLSession.shared.data(for: request)
            let status = (response as? HTTPURLResponse)?.statusCode ?? 500
            let decoder = JSONDecoder()
            decoder.keyDecodingStrategy = .convertFromSnakeCase
            // An older server without the endpoint still answered.
            result = status < 500 ? .reachable(try? decoder.decode(ServerInfo.self, from: data)) : .unreachable
        } catch {
            result = .unreachable
        }
        guard !Task.isCancelled else { return }
        probe = result
    }

    // MARK: Advanced

    private var advanced: some View {
        VStack(alignment: .leading, spacing: 10) {
            Button {
                showAdvanced.toggle()
            } label: {
                HStack(spacing: 6) {
                    Text("Advanced")
                        .font(.system(size: 14, weight: .semibold))
                    Image(systemName: "chevron.down")
                        .font(.system(size: 10, weight: .bold))
                        .rotationEffect(.degrees(showAdvanced ? 180 : 0))
                }
                .foregroundStyle(WTheme.inkSecondary)
                .padding(.horizontal, 6)
                .contentShape(Rectangle())
            }
            .buttonStyle(PressableButtonStyle())

            if showAdvanced {
                VStack(spacing: 0) {
                    if asksBypassToken {
                        advancedField(
                            "Captcha bypass",
                            placeholder: "Dev bypass token",
                            text: $turnstile,
                            field: .turnstile
                        )
                        Rectangle()
                            .fill(WTheme.hairlineStrong)
                            .frame(height: 0.5)
                            .padding(.leading, 14)
                    }
                    advancedField(
                        "Realtime server",
                        placeholder: "Same as server",
                        text: $realtime,
                        field: .realtime
                    )
                    .keyboardType(.URL)
                }
                .background(WTheme.groupedCard, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
                .transition(.opacity.combined(with: .move(edge: .top)))

                Text(advancedHint)
                    .font(.system(size: 12.5))
                    .foregroundStyle(WTheme.inkTertiary)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.horizontal, 6)
                    .transition(.opacity)
            }
        }
    }

    private func advancedField(_ title: String, placeholder: String, text: Binding<String>, field: Field) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(title)
                .font(.system(size: 12, weight: .medium))
                .foregroundStyle(focused == field ? WTheme.accent : WTheme.inkSecondary)
            TextField(placeholder, text: text)
                .font(.system(size: 15))
                .foregroundStyle(WTheme.ink)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .focused($focused, equals: field)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 11)
        .contentShape(Rectangle())
        .onTapGesture { focused = field }
        .animation(.easeOut(duration: 0.18), value: focused == field)
    }

    // MARK: Save

    private var saveBar: some View {
        Button(action: save) {
            Text(choice == .cloud ? "Use Warmbly Cloud" : "Save")
                .font(.system(size: 16.5, weight: .semibold))
                .foregroundStyle(WTheme.inkInverse)
                .contentTransition(.opacity)
                .frame(maxWidth: .infinity)
                .frame(height: 54)
                .background(WTheme.ink.opacity(resolvedOrigin == nil ? 0.18 : 1), in: Capsule())
        }
        .buttonStyle(PressableButtonStyle())
        .disabled(resolvedOrigin == nil)
        .padding(.horizontal, 20)
        .padding(.top, 10)
        .padding(.bottom, 8)
        .background(WTheme.groupedPage)
    }

    private var advancedHint: String {
        if emailSignInOff, choice == .selfHosted {
            return "This server asks for a captcha but takes no App Attest proof. On a dev backend, enter its TURNSTILE_BYPASS_TOKEN; otherwise sign in with Apple, Google or a passkey, or set APP_ATTEST_APP_IDS on the server."
        }
        return asksBypassToken
            ? "The bypass token is the dev stack's TURNSTILE_BYPASS_TOKEN. The realtime field is only needed when live updates run on a separate host."
            : "Only needed when your server runs live updates on a separate host."
    }

    private func select(_ value: Choice) {
        guard choice != value else { return }
        choice = value
        switch value {
        case .cloud:
            focused = nil
            showAdvanced = false
        case .selfHosted:
            if turnstile == AppConfig.localTurnstileToken { turnstile = "" }
            if customOrigin.isEmpty { focused = .origin }
        case .local:
            focused = nil
            if turnstile.isEmpty { turnstile = AppConfig.localTurnstileToken }
        }
    }

    private func save() {
        guard let origin = resolvedOrigin else { return }
        AppConfig.serverOrigin = origin
        if choice == .cloud {
            AppConfig.turnstileToken = ""
            AppConfig.realtimeOriginOverride = ""
        } else {
            AppConfig.turnstileToken = asksBypassToken ? turnstile.trimmingCharacters(in: .whitespaces) : ""
            AppConfig.realtimeOriginOverride = AppConfig.normalizeOrigin(realtime)
        }
        saved += 1
        dismiss()
    }
}

/// A radio dot that fills with a tick when chosen.
private struct RadioMark: View {
    let on: Bool

    var body: some View {
        ZStack {
            Circle()
                .strokeBorder(WTheme.hairlineStrong, lineWidth: 1.5)
                .opacity(on ? 0 : 1)
            Circle()
                .fill(WTheme.accent)
                .scaleEffect(on ? 1 : 0.3)
                .opacity(on ? 1 : 0)
            Image(systemName: "checkmark")
                .font(.system(size: 11, weight: .bold))
                .foregroundStyle(.white)
                .scaleEffect(on ? 1 : 0.4)
                .opacity(on ? 1 : 0)
        }
        .frame(width: 22, height: 22)
        .animation(.spring(response: 0.34, dampingFraction: 0.62), value: on)
    }
}
