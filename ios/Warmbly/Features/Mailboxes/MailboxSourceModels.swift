import Foundation

// Where mailboxes come from besides one typed login: admin grants over a whole
// Google Workspace domain or Microsoft 365 organization, inbox vendor accounts,
// file imports, and the background import job each of them starts.

/// `GET /emails/grants/config`: which admin connections this instance can make.
struct MailboxGrantConfig: Decodable, Sendable {
    var googleEnabled: Bool
    var microsoftEnabled: Bool
    var googleMissing: [String]?
    var microsoftMissing: [String]?

    enum CodingKeys: String, CodingKey {
        case googleEnabled = "google_enabled"
        case microsoftEnabled = "microsoft_enabled"
        case googleMissing = "google_missing"
        case microsoftMissing = "microsoft_missing"
    }

    func enabled(_ provider: GrantProvider) -> Bool {
        provider == .google ? googleEnabled : microsoftEnabled
    }

    func missing(_ provider: GrantProvider) -> [String] {
        (provider == .google ? googleMissing : microsoftMissing) ?? []
    }
}

enum GrantProvider: String, Codable, Sendable {
    case google, microsoft

    var label: String { self == .google ? "Google" : "Microsoft" }
    var mark: MailboxBrand.Mark { self == .google ? .google : .microsoft }
}

/// One administrator's grant over a domain or tenant.
struct MailboxGrant: Decodable, Identifiable, Sendable, Hashable {
    var id: String
    var provider: GrantProvider
    var tenant: String
    var adminEmail: String?
    var domains: [String]?
    var status: String
    var lastError: String?
    var mailboxes: Int?

    enum CodingKeys: String, CodingKey {
        case id, provider, tenant, domains, status, mailboxes
        case adminEmail = "admin_email"
        case lastError = "last_error"
    }

    var isActive: Bool { status == "active" }

    /// The Google domain, or the Microsoft tenant's domains.
    var name: String {
        if provider == .google { return tenant }
        let list = domains ?? []
        return list.isEmpty ? tenant : list.joined(separator: ", ")
    }
}

/// One account in a granted domain's directory.
struct GrantDirectoryUser: Decodable, Identifiable, Sendable, Hashable {
    var id: String
    var email: String
    var name: String
    var enabled: Bool
    var connected: Bool
    var upgrade: Bool?

    /// Connected already, or suspended in the directory.
    var selectable: Bool { enabled && !connected }
}

/// `POST /emails/grants/:id/connect` body.
struct GrantConnectBody: Encodable {
    struct Settings: Encodable { var warmup: Bool? }
    struct Options: Encodable {
        var onExisting = "update"
        var settings: Settings

        enum CodingKeys: String, CodingKey {
            case onExisting = "on_existing"
            case settings
        }
    }

    var userIDs: [String]
    var options: Options

    enum CodingKeys: String, CodingKey {
        case userIDs = "user_ids"
        case options
    }
}

/// `GET /emails/imports/:id`: a background connect the backend works row by row.
struct MailboxImportJob: Decodable, Sendable, Equatable {
    struct Cause: Decodable, Sendable, Equatable, Hashable {
        var cause: String
        var title: String
        var fix: String
        var count: Int
    }

    var id: String
    var status: String
    var total: Int
    var counts: [String: Int]
    var causes: [Cause]?

    var isRunning: Bool { status == "running" }

    func count(_ key: String) -> Int { counts[key] ?? 0 }

    /// Rows that have reached an outcome.
    var finished: Int {
        ["connected", "updated", "skipped", "failed", "needs_signin", "cancelled"].reduce(0) { $0 + count($1) }
    }
}

/// `POST /auth/reauth` body: a password or a two-factor code.
struct ReauthBody: Encodable {
    var password: String?
    var code: String?
}

/// `GET /auth/reauth/methods`: the ways this account can confirm a recent sign-in.
struct ReauthMethods: Decodable, Sendable {
    var password: Bool
    var totp: Bool
    var passkey: Bool
    /// Linked identities the server can verify a native ID token for ("apple", "google").
    var nativeProviders: [String]

    enum CodingKeys: String, CodingKey {
        case password, totp, passkey
        case nativeProviders = "native_providers"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        password = try c.decodeIfPresent(Bool.self, forKey: .password) ?? false
        totp = try c.decodeIfPresent(Bool.self, forKey: .totp) ?? false
        passkey = try c.decodeIfPresent(Bool.self, forKey: .passkey) ?? false
        nativeProviders = try c.decodeIfPresent([String].self, forKey: .nativeProviders) ?? []
    }
}

/// `POST /auth/reauth/native/begin`: the single-use nonce the provider signs into its ID token.
struct ReauthNativeBegin: Decodable, Sendable {
    var nonce: String
}

/// `POST /auth/reauth/native` body.
struct ReauthNativeBody: Encodable {
    var provider: String
    var idToken: String

    enum CodingKeys: String, CodingKey {
        case provider
        case idToken = "id_token"
    }
}

// MARK: - Inbox vendors (`/emails/vendors`)

struct VendorField: Decodable, Sendable, Hashable {
    var key: String
    var label: String
    var secret: Bool
    var required: Bool
    var help: String?
}

/// A vendor the backend can read mailboxes from.
struct VendorCatalogEntry: Decodable, Identifiable, Sendable, Hashable {
    var id: String
    var label: String
    var website: String?
    var keyHelpURL: String?
    var fields: [VendorField]

    enum CodingKeys: String, CodingKey {
        case id, label, website, fields
        case keyHelpURL = "key_help_url"
    }
}

/// A vendor account the workspace imports from. Its key never leaves the server.
struct VendorConnection: Decodable, Identifiable, Sendable, Hashable {
    var id: String
    var vendor: String
    var label: String
    var status: String
    var lastError: String?
    var mailboxes: Int?

    enum CodingKeys: String, CodingKey {
        case id, vendor, label, status, mailboxes
        case lastError = "last_error"
    }

    var isActive: Bool { status != "invalid" }
}

struct VendorMailbox: Decodable, Identifiable, Sendable, Hashable {
    var id: String
    var email: String
    var name: String
    var provider: String?
    var status: String?
    var workspace: String?
    var connected: Bool
}

/// `POST /emails/vendors` body.
struct CreateVendorBody: Encodable {
    var vendor: String
    var label: String
    var fields: [String: String]
}

/// `PATCH /emails/vendors/:id` body: a replacement key.
struct UpdateVendorBody: Encodable {
    var fields: [String: String]
}

/// `POST /emails/vendors/:id/import` body.
struct VendorImportBody: Encodable {
    var mailboxIDs: [String]
    var options: GrantConnectBody.Options

    enum CodingKeys: String, CodingKey {
        case mailboxIDs = "mailbox_ids"
        case options
    }
}

// MARK: - File import (`/emails/imports`)

/// `POST /emails/imports/preview`: what importing a file would do, writing nothing.
struct MailboxImportPreview: Decodable, Sendable {
    struct Summary: Decodable, Sendable {
        var total: Int
        var ready: Int
        var needsSignin: Int
        var invalid: Int
        var existing: Int
        var duplicate: Int

        enum CodingKeys: String, CodingKey {
            case total, ready, invalid, existing, duplicate
            case needsSignin = "needs_signin"
        }
    }

    struct Domain: Decodable, Sendable, Hashable {
        var domain: String
        var mailHost: String?
        var label: String?
        var rows: Int
        var passwordAuth: String?
        var appPasswordURL: String?

        enum CodingKeys: String, CodingKey {
            case domain, label, rows
            case mailHost = "mail_host"
            case passwordAuth = "password_auth"
            case appPasswordURL = "app_password_url"
        }
    }

    struct Issue: Decodable, Sendable, Hashable {
        var cause: String
        var title: String
        var fix: String
        var count: Int
    }

    struct Vendor: Decodable, Sendable {
        var id: String
        var label: String
    }

    var format: String?
    var hasHeader: Bool?
    var vendor: Vendor?
    var mapping: [String: String]?
    var summary: Summary
    var domains: [Domain]?
    var issues: [Issue]?

    enum CodingKeys: String, CodingKey {
        case format, vendor, mapping, summary, domains, issues
        case hasHeader = "has_header"
    }

    /// The file names a password for every row, or the import needs one shared.
    var mapsPassword: Bool {
        let fields: Set<String> = ["password", "app_password", "smtp_password", "imap_password"]
        return (mapping ?? [:]).values.contains { fields.contains($0) }
    }
}

/// The `options` JSON field of a file preview or import.
struct FileImportOptions: Encodable {
    var hasHeader: Bool?
    var sharedPassword: String?
    var onExisting: String?
    var saveMapping: Bool?
    var settings: GrantConnectBody.Settings?

    enum CodingKeys: String, CodingKey {
        case settings
        case hasHeader = "has_header"
        case sharedPassword = "shared_password"
        case onExisting = "on_existing"
        case saveMapping = "save_mapping"
    }
}

// MARK: - Warmbly Cloud (`/cloud-link/workspace-mailboxes`)

/// A mailbox connected on the linked Warmbly Cloud workspace, ready to adopt here.
struct CloudWorkspaceMailbox: Decodable, Identifiable, Sendable, Hashable {
    var id: String
    var email: String
    var name: String
    var provider: String
    var status: String?
}
