import type { AuthenticationResponseJSON, PublicKeyCredentialRequestOptionsJSON } from "@simplewebauthn/browser";
import Request from "../../Request";

export interface ReauthPasskeyChallenge {
    options: { publicKey: PublicKeyCredentialRequestOptionsJSON };
}

/** Asks for one of the signed-in account's own passkeys. */
export function beginPasskeyReauth() {
    return Request<ReauthPasskeyChallenge>({
        method: "post",
        url: "/auth/reauth/passkey/begin",
        authorization: true,
        skipReauthPrompt: true,
    });
}

export function finishPasskeyReauth(credential: AuthenticationResponseJSON) {
    return Request<{ valid_for_seconds: number }>({
        method: "post",
        url: "/auth/reauth/passkey/finish",
        data: { credential },
        authorization: true,
        skipReauthPrompt: true,
    });
}
