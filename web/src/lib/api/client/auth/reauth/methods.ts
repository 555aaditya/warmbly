import Request from "../../Request";
import type ReauthMethods from "@/lib/api/models/auth/ReauthMethods";

export default function getReauthMethods(signal?: AbortSignal) {
    return Request<ReauthMethods>({
        method: "get",
        url: "/auth/reauth/methods",
        authorization: true,
        skipReauthPrompt: true,
        signal,
    });
}
