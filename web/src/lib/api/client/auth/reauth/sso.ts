import Request from "../../Request";

/** Starts a provider window for the account's linked identity; the binding stays in this tab. */
export function beginSSOReauth(provider: string) {
    return Request<{ url: string; binding: string }>({
        method: "post",
        url: "/auth/reauth/sso/begin",
        data: { provider },
        authorization: true,
        skipReauthPrompt: true,
    });
}

export function finishSSOReauth(code: string, binding: string) {
    return Request<{ valid_for_seconds: number }>({
        method: "post",
        url: "/auth/reauth/sso/finish",
        data: { code, binding },
        authorization: true,
        skipReauthPrompt: true,
    });
}
