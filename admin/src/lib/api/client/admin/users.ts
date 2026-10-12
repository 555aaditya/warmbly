// /admin/users/* — platform user admin. Search, profile, ban/unban,
// rate-limit overrides. All endpoints already exist on the backend.

import { Request } from "@/lib/api/client";
import { buildSearchQuery } from "@/lib/api/client/admin/query";
import type {
    AdminUserDetail,
    AdminUserPreview,
    AdminUserRateLimits,
    AdminUserSearchParams,
    AdminUsersResult,
    BanUserRequest,
    UnbanUserRequest,
    UpdateUserRateLimitsRequest,
    UserBan,
} from "@/lib/api/models/admin";

function toQuery(params: AdminUserSearchParams): string {
    return buildSearchQuery(params as Record<string, unknown>);
}

export function searchUsers(
    params: AdminUserSearchParams = {},
): Promise<AdminUsersResult> {
    return Request({
        method: "GET",
        url: `/admin/users${toQuery(params)}`,
        authorization: true,
    });
}

export function getUser(id: string): Promise<AdminUserDetail> {
    return Request({
        method: "GET",
        url: `/admin/users/${id}`,
        authorization: true,
    });
}

export function getUserPreview(id: string): Promise<AdminUserPreview> {
    return Request({
        method: "GET",
        url: `/admin/users/${id}/preview`,
        authorization: true,
    });
}

export function getUserBans(id: string): Promise<{ data: UserBan[] }> {
    return Request({
        method: "GET",
        url: `/admin/users/${id}/bans`,
        authorization: true,
    });
}

export function banUser(id: string, body: BanUserRequest): Promise<void> {
    return Request({
        method: "POST",
        url: `/admin/users/${id}/ban`,
        authorization: true,
        data: body,
    });
}

export function unbanUser(id: string, body: UnbanUserRequest): Promise<void> {
    return Request({
        method: "POST",
        url: `/admin/users/${id}/unban`,
        authorization: true,
        data: body,
    });
}

// Always resolves: a user with no override row reads back the product defaults.
export function getUserRateLimits(id: string): Promise<AdminUserRateLimits> {
    return Request({
        method: "GET",
        url: `/admin/users/${id}/rate-limits`,
        authorization: true,
    });
}

export function updateUserRateLimits(
    id: string,
    body: UpdateUserRateLimitsRequest,
): Promise<AdminUserRateLimits> {
    return Request({
        method: "PATCH",
        url: `/admin/users/${id}/rate-limits`,
        authorization: true,
        data: body,
    });
}

/** Grants (reason required) or clears the emailed sign-in code exemption.
 *  Clearing also signs the account out everywhere. */
export function setUserLoginCodeExempt(
    id: string,
    body: { exempt: true; reason: string } | { exempt: false },
): Promise<{ login_code_exempt: boolean; password_cleared?: boolean }> {
    return Request({
        method: "PUT",
        url: `/admin/users/${id}/login-code-exempt`,
        authorization: true,
        data: body,
    });
}
