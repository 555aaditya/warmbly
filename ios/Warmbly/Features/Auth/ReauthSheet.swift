import AuthenticationServices
import SwiftUI

/// Confirms the account holder before a change that needs a recent sign-in,
/// then hands control back so the caller retries. Offers only the ways this
/// account can confirm (`GET auth/reauth/methods`), best first.
struct ReauthSheet: View {
    var reason = "Connecting a whole domain's mailboxes needs a recent sign-in."
    var actionTitle = "Confirm and connect"
    var onConfirmed: () -> Void

    private enum Phase {
        case loading
        case loaded(ReauthMethods)
        /// Methods could not be read, so the password and code form stays as it was.
        case fallback
    }

    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @Environment(\.colorScheme) private var colorScheme
    @State private var phase: Phase = .loading
    @State private var googleClientID: String?
    @State private var secret = ""
    @State private var useCode = false
    @State private var busy = false
    @State private var nativeBusy = false
    @State private var errorMessage: String?
    @State private var contentHeight: CGFloat = 420
    @State private var appleFlow = AppleIDReauthFlow()
    @State private var googleFlow = GoogleSignInFlow()
    @FocusState private var focused: Bool

    private var methods: ReauthMethods? {
        if case let .loaded(methods) = phase { return methods }
        return nil
    }

    private var isLoading: Bool {
        if case .loading = phase { return true }
        return false
    }

    private var showApple: Bool { methods?.nativeProviders.contains("apple") == true }
    private var showGoogle: Bool { methods?.nativeProviders.contains("google") == true && googleClientID != nil }
    private var allowsPassword: Bool { methods?.password ?? true }
    private var allowsCode: Bool { methods?.totp ?? true }
    private var showsField: Bool { !isLoading && (allowsPassword || allowsCode) }
    private var hasProviders: Bool { showApple || showGoogle }
    private var hasNothing: Bool { !isLoading && !showsField && !hasProviders }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
                .padding(.bottom, 22)

            if isLoading {
                ProgressView()
                    .frame(maxWidth: .infinity)
                    .frame(height: 54)
            } else if hasNothing {
                Text("This account has no way to confirm yet. Sign out and sign in again, then repeat this within five minutes.")
                    .font(.system(size: 14.5))
                    .foregroundStyle(WTheme.ink)
                    .fixedSize(horizontal: false, vertical: true)
            } else {
                if hasProviders { providerButtons }
                if hasProviders && showsField { orDivider }
                if showsField { secretField }
            }

            if let errorMessage {
                Text(errorMessage)
                    .font(.system(size: 13.5))
                    .foregroundStyle(WTheme.negative)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.top, 12)
                    .transition(.opacity)
            }

            if hasNothing {
                Button("Close") { dismiss() }
                    .buttonStyle(InkButtonStyle(size: .large, fullWidth: true))
                    .padding(.top, 24)
            } else if showsField {
                Button {
                    Task { await confirm() }
                } label: {
                    if busy {
                        ProgressView().tint(WTheme.inkInverse)
                    } else {
                        Text(actionTitle)
                    }
                }
                .buttonStyle(InkButtonStyle(size: .large, fullWidth: true))
                .disabled(secret.isEmpty || nativeBusy)
                .allowsHitTesting(!busy)
                .padding(.top, 24)
            }
        }
        .padding(24)
        .fixedSize(horizontal: false, vertical: true)
        .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { contentHeight = $0 }
        .frame(maxHeight: .infinity, alignment: .top)
        .background(WTheme.canvas)
        .presentationDetents([.height(contentHeight)])
        .presentationDragIndicator(.visible)
        .sensoryFeedback(.error, trigger: errorMessage)
        .animation(.snappy, value: isLoading)
        .task { await loadMethods() }
    }

    // MARK: Sections

    private var header: some View {
        HStack(alignment: .top) {
            VStack(alignment: .leading, spacing: 5) {
                Text("Confirm it's you")
                    .displayFont(26)
                    .foregroundStyle(WTheme.ink)
                Text(reason)
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
    }

    private var providerButtons: some View {
        VStack(spacing: 12) {
            if showApple {
                Button {
                    Task { await confirmWithApple() }
                } label: {
                    HStack(spacing: 8) {
                        Image(systemName: "applelogo")
                            .font(.system(size: 18, weight: .medium))
                        Text("Continue with Apple")
                            .font(.system(size: 16.5, weight: .medium))
                    }
                    .foregroundStyle(colorScheme == .dark ? Color.black : Color.white)
                    .frame(maxWidth: .infinity)
                    .frame(height: 54)
                    .background(colorScheme == .dark ? Color.white : Color.black, in: Capsule())
                }
                .buttonStyle(PressableButtonStyle())
            }

            if showGoogle, let googleClientID {
                Button {
                    Task { await confirmWithGoogle(clientID: googleClientID) }
                } label: {
                    HStack(spacing: 10) {
                        GoogleGlyph()
                            .frame(width: 19, height: 19)
                        Text("Continue with Google")
                            .font(.system(size: 16.5, weight: .medium))
                            .foregroundStyle(WTheme.ink)
                    }
                    .frame(maxWidth: .infinity)
                    .frame(height: 54)
                    .background(WTheme.raised, in: Capsule())
                    .overlay(Capsule().strokeBorder(WTheme.hairlineStrong, lineWidth: 1))
                }
                .buttonStyle(PressableButtonStyle())
            }
        }
        .disabled(busy || nativeBusy)
    }

    private var orDivider: some View {
        HStack(spacing: 12) {
            Rectangle().fill(WTheme.hairlineStrong).frame(height: 1)
            Text("or")
                .font(.system(size: 13))
                .foregroundStyle(WTheme.inkTertiary)
            Rectangle().fill(WTheme.hairlineStrong).frame(height: 1)
        }
        .padding(.vertical, 18)
    }

    private var secretField: some View {
        VStack(alignment: .leading, spacing: 0) {
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

            if allowsPassword && allowsCode {
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
            }
        }
    }

    // MARK: Actions

    private func loadMethods() async {
        do {
            let loaded: ReauthMethods = try await env.api.get("auth/reauth/methods")
            if loaded.nativeProviders.contains("google") {
                googleClientID = await env.session.fetchAuthProviders()?.googleClientID
            }
            useCode = !loaded.password && loaded.totp
            phase = .loaded(loaded)
            // A provider button is the better first choice, so the keyboard waits for it.
            if !loaded.nativeProviders.contains("apple") && !(loaded.nativeProviders.contains("google") && googleClientID != nil) {
                focused = loaded.password || loaded.totp
            }
        } catch {
            phase = .fallback
            focused = true
        }
    }

    private func confirm() async {
        guard !secret.isEmpty, !busy, !nativeBusy else { return }
        busy = true
        defer { busy = false }
        let body = useCode ? ReauthBody(code: secret.filter(\.isNumber)) : ReauthBody(password: secret)
        do {
            let _: EmptyBody = try await env.api.post("auth/reauth", body: body)
            finish()
        } catch {
            show(error)
        }
    }

    private func confirmWithApple() async {
        await confirmNative(provider: "apple") { nonce in
            try await appleFlow.identityToken(nonce: nonce)
        }
    }

    private func confirmWithGoogle(clientID: String) async {
        await confirmNative(provider: "google") { nonce in
            try await googleFlow.signIn(clientID: clientID, nonce: nonce)
        }
    }

    /// Begin, run the provider sheet with the session's nonce, then hand the ID token back.
    private func confirmNative(provider: String, idToken: (String) async throws -> String) async {
        guard !busy, !nativeBusy else { return }
        nativeBusy = true
        defer { nativeBusy = false }
        errorMessage = nil
        do {
            let begin: ReauthNativeBegin = try await env.api.post("auth/reauth/native/begin")
            let token = try await idToken(begin.nonce)
            let _: EmptyBody = try await env.api.post(
                "auth/reauth/native",
                body: ReauthNativeBody(provider: provider, idToken: token)
            )
            finish()
        } catch AppleIDReauthFlow.Failure.cancelled {
            // Closing the Apple sheet is not an error worth surfacing.
        } catch GoogleSignInFlow.Failure.cancelled {
            // Closing the Google sheet is not an error worth surfacing.
        } catch {
            show(error)
        }
    }

    private func finish() {
        dismiss()
        onConfirmed()
    }

    private func show(_ error: Error) {
        withAnimation(.easeOut(duration: 0.2)) {
            errorMessage = (error as? APIError)?.errorDescription ?? error.localizedDescription
        }
    }
}

/// Native Sign in with Apple run for its ID token alone, with a nonce the
/// server minted, so the token proves a fresh sign-in to this session.
@MainActor
final class AppleIDReauthFlow: NSObject, ASAuthorizationControllerDelegate, ASAuthorizationControllerPresentationContextProviding {
    enum Failure: Error {
        case cancelled
        case malformedResponse
    }

    private var controller: ASAuthorizationController?
    private var presentationWindow: UIWindow?
    private var continuation: CheckedContinuation<String, Error>?

    func identityToken(nonce: String) async throws -> String {
        guard let window = Self.keyWindow else { throw Failure.malformedResponse }
        presentationWindow = window
        defer { presentationWindow = nil }
        let request = ASAuthorizationAppleIDProvider().createRequest()
        // Passed verbatim: the server compares the token's nonce claim to the raw string.
        request.nonce = nonce
        let controller = ASAuthorizationController(authorizationRequests: [request])
        controller.delegate = self
        controller.presentationContextProvider = self
        self.controller = controller
        return try await withCheckedThrowingContinuation { continuation in
            self.continuation = continuation
            controller.performRequests()
        }
    }

    private func resume(_ result: Result<String, Error>) {
        controller = nil
        continuation?.resume(with: result)
        continuation = nil
    }

    nonisolated func authorizationController(controller: ASAuthorizationController, didCompleteWithAuthorization authorization: ASAuthorization) {
        let token = (authorization.credential as? ASAuthorizationAppleIDCredential)?
            .identityToken
            .flatMap { String(data: $0, encoding: .utf8) }
        MainActor.assumeIsolated {
            if let token {
                resume(.success(token))
            } else {
                resume(.failure(Failure.malformedResponse))
            }
        }
    }

    nonisolated func authorizationController(controller: ASAuthorizationController, didCompleteWithError error: Error) {
        let cancelled = (error as? ASAuthorizationError)?.code == .canceled
        MainActor.assumeIsolated {
            resume(.failure(cancelled ? Failure.cancelled : error))
        }
    }

    nonisolated func presentationAnchor(for controller: ASAuthorizationController) -> ASPresentationAnchor {
        MainActor.assumeIsolated {
            // identityToken guards that a window exists before the request starts.
            presentationWindow ?? Self.keyWindow!
        }
    }

    private static var keyWindow: UIWindow? {
        let windows = UIApplication.shared.connectedScenes
            .compactMap { $0 as? UIWindowScene }
            .flatMap(\.windows)
        return windows.first(where: \.isKeyWindow) ?? windows.first
    }
}
