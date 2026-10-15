// GET /auth/reauth/methods: every way this account can confirm it is you.
export default interface ReauthMethods {
    password: boolean;
    totp: boolean;
    passkey: boolean;
    // Linked identities this deployment can re-verify in a browser window.
    providers: { id: string; label: string }[];
    native_providers: string[];
    valid_for_seconds: number;
}
