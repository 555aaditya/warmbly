import CryptoKit
import DeviceCheck
import Foundation

/// Proves an email sign-in comes from this app on a real device (App Attest),
/// which the server accepts in place of a captcha. One key per server.
actor AppAttestClient {
    /// The `app_attest` object sent with a protected auth call.
    struct Proof: Encodable, Sendable {
        let keyID: String
        let assertion: String
        let challenge: String

        enum CodingKeys: String, CodingKey {
            case keyID = "key_id"
            case assertion, challenge
        }
    }

    /// The request a proof is made for; the server checks it matches.
    enum Action: String, Sendable {
        case login
        case register
        case resetPassword = "reset_password"
    }

    enum Failure: LocalizedError {
        case unsupported

        var errorDescription: String? {
            "This device can't verify the app. Sign in with Apple or Google instead."
        }
    }

    private struct Challenge: Decodable { let challenge: String }

    private struct KeyBody: Encodable {
        let keyID: String
        let attestation: String
        let challenge: String

        enum CodingKeys: String, CodingKey {
            case keyID = "key_id"
            case attestation, challenge
        }
    }

    private let api: APIClient
    private var registering: Task<String, Error>?

    init(api: APIClient) {
        self.api = api
    }

    static var isSupported: Bool { DCAppAttestService.shared.isSupported }

    private static var storageKey: String { "app_attest_key." + AppConfig.serverOrigin }

    /// Signs a fresh challenge for action with this install's key, registering it first if needed.
    func proof(for action: Action) async throws -> Proof {
        guard Self.isSupported else { throw Failure.unsupported }
        let keyID = try await registeredKey()
        let challenge = try await fetchChallenge()
        let clientData = Data("warmbly-app-attest/v1\n\(action.rawValue)\n\(challenge)".utf8)
        do {
            let assertion = try await DCAppAttestService.shared.generateAssertion(keyID, clientDataHash: Self.hash(clientData))
            return Proof(keyID: keyID, assertion: assertion.base64EncodedString(), challenge: challenge)
        } catch let error as DCError where error.code == .invalidKey {
            // The system dropped the key (a restore onto another device); the next proof makes a new one.
            forgetKey()
            throw error
        }
    }

    /// Drops the stored key, so the next proof registers a new one.
    func forgetKey() {
        UserDefaults.standard.removeObject(forKey: Self.storageKey)
    }

    private func registeredKey() async throws -> String {
        if let keyID = UserDefaults.standard.string(forKey: Self.storageKey) { return keyID }
        if let registering { return try await registering.value }
        let task = Task { try await self.register() }
        registering = task
        defer { registering = nil }
        let keyID = try await task.value
        UserDefaults.standard.set(keyID, forKey: Self.storageKey)
        return keyID
    }

    private func register() async throws -> String {
        let service = DCAppAttestService.shared
        let keyID = try await service.generateKey()
        let challenge = try await fetchChallenge()
        let attestation = try await service.attestKey(keyID, clientDataHash: Self.hash(Data(challenge.utf8)))
        let body = KeyBody(keyID: keyID, attestation: attestation.base64EncodedString(), challenge: challenge)
        let _: EmptyBody = try await api.post("auth/app-attest/keys", body: body, authorized: false)
        return keyID
    }

    private func fetchChallenge() async throws -> String {
        let response: Challenge = try await api.post("auth/app-attest/challenge", authorized: false)
        return response.challenge
    }

    private static func hash(_ data: Data) -> Data {
        Data(SHA256.hash(data: data))
    }
}
