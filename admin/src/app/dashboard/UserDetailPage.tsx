// User detail: composes /admin/users/:id/preview into one screen. The main
// column holds the profile, usage, orgs, mailboxes and ban history; the
// right column holds the properties.

import { useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
    Ban,
    Building2,
    CheckCircle2,
    Gauge,
    Mail,
    Megaphone,
    MoreHorizontal,
    ShieldAlert,
} from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
    Property,
    PropertyList,
    Section,
    Stat,
    StatGrid,
    StatusBadge,
    StatusDot,
} from "@/components/ui/kit";
import { ErrorState } from "@/components/ErrorState";
import type { Tone } from "@/lib/tones";
import { cn } from "@/lib/utils";
import {
    getUserBans,
    getUserPreview,
} from "@/lib/api/client/admin/users";
import type { UserBan } from "@/lib/api/models/admin";
import { UserBanDialog } from "./UserBanDialog";
import { UserRateLimitsDialog } from "./UserRateLimitsDialog";
import { UserLoginCodeCard } from "./UserLoginCodeCard";

const CRUMBS = [{ label: "Users", to: "/users" }];

function mailboxTone(status: string): Tone {
    const s = status.toLowerCase();
    if (s === "active") return "success";
    if (/(error|block|ban|suspend|fail|revoked)/.test(s)) return "danger";
    if (/(pause|pending|warn)/.test(s)) return "warning";
    return "neutral";
}

function initialsOf(first: string, last: string, email: string): string {
    const a = (first || "").trim()[0] ?? "";
    const b = (last || "").trim()[0] ?? "";
    return (a + b || email[0] || "?").toUpperCase();
}

export default function UserDetailPage() {
    const { id = "" } = useParams<{ id: string }>();
    const nav = useNavigate();
    const [banDialog, setBanDialog] = useState<"ban" | "unban" | null>(null);
    const [rateLimitsOpen, setRateLimitsOpen] = useState(false);

    const previewQuery = useQuery({
        queryKey: ["admin", "users", id],
        queryFn: () => getUserPreview(id),
        enabled: !!id,
    });

    const bansQuery = useQuery({
        queryKey: ["admin", "users", id, "bans"],
        queryFn: () => getUserBans(id),
        enabled: !!id,
    });

    if (previewQuery.isLoading) return <DetailSkeleton />;
    if (previewQuery.error || !previewQuery.data) {
        return (
            <div>
                <PageHeader breadcrumbs={CRUMBS} title="User" />
                <ErrorState
                    error={previewQuery.error}
                    title="Failed to load user."
                    onRetry={() => previewQuery.refetch()}
                />
            </div>
        );
    }

    const preview = previewQuery.data;
    const u = preview.user;
    // An instance that has not picked up the empty-slice fix still answers
    // null for these, and null.length is what took the whole page down.
    const organizations = preview.organizations ?? [];
    const mailboxes = preview.email_accounts ?? [];
    const banned = !!u.banned_at;
    const isAdmin = u.admin_permissions > 0;
    const fullName = `${u.first_name} ${u.last_name}`.trim() || u.email;
    const bans = bansQuery.data?.data ?? [];
    const customLimits = !!preview.rate_limits?.updated_at;

    const statusBadge = banned ? (
        <StatusBadge tone="danger" dot>
            Banned
        </StatusBadge>
    ) : (
        <StatusBadge tone="success" dot>
            Active
        </StatusBadge>
    );

    return (
        <div>
            <PageHeader
                breadcrumbs={CRUMBS}
                title={fullName}
                meta={
                    <>
                        {statusBadge}
                        {isAdmin && (
                            <StatusBadge tone="accent">
                                <ShieldAlert className="size-3" />
                                Admin
                            </StatusBadge>
                        )}
                        {u.login_code_exempt && <StatusBadge tone="warning">Skips sign-in code</StatusBadge>}
                    </>
                }
            >
                <Button size="sm" variant="outline" onClick={() => setRateLimitsOpen(true)}>
                    <Gauge className="size-3.5" />
                    Rate limits
                </Button>
                {banned && (
                    <Button size="sm" variant="outline" onClick={() => setBanDialog("unban")}>
                        <CheckCircle2 className="size-3.5 text-emerald-600 dark:text-emerald-400" />
                        Unban
                    </Button>
                )}
                <DropdownMenu modal={false}>
                    <DropdownMenuTrigger asChild>
                        <Button size="icon-sm" variant="ghost" aria-label="More actions">
                            <MoreHorizontal className="size-4" />
                        </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end" className="w-52">
                        <DropdownMenuItem onSelect={() => setRateLimitsOpen(true)}>
                            <Gauge />
                            Edit rate limits
                        </DropdownMenuItem>
                        <DropdownMenuSeparator />
                        {banned ? (
                            <DropdownMenuItem onSelect={() => setBanDialog("unban")}>
                                <CheckCircle2 />
                                Unban user
                            </DropdownMenuItem>
                        ) : (
                            <DropdownMenuItem
                                variant="destructive"
                                disabled={isAdmin}
                                onSelect={() => setBanDialog("ban")}
                            >
                                <Ban />
                                Ban user
                                {isAdmin && (
                                    <span className="ml-auto text-xs text-subtle-foreground">Admin</span>
                                )}
                            </DropdownMenuItem>
                        )}
                    </DropdownMenuContent>
                </DropdownMenu>
            </PageHeader>

            <div className="grid gap-8 lg:grid-cols-[minmax(0,1fr)_280px] lg:gap-10">
                <div className="min-w-0">
                    <div className="flex items-center gap-3">
                        <span
                            aria-hidden
                            className="grid size-10 shrink-0 place-items-center rounded-full border border-border bg-muted text-[13px] font-semibold text-muted-foreground"
                        >
                            {initialsOf(u.first_name, u.last_name, u.email)}
                        </span>
                        <div className="min-w-0">
                            <div className="truncate text-[18px] leading-6 font-semibold tracking-[-0.01em] text-foreground">
                                {fullName}
                            </div>
                            <div className="truncate text-[13px] text-muted-foreground">{u.email}</div>
                        </div>
                    </div>

                    <StatGrid className="mt-6 grid-cols-3 md:grid-cols-3">
                        <Stat label="Organizations" icon={Building2} value={u.organization_count} />
                        <Stat label="Mailboxes" icon={Mail} value={u.email_account_count} />
                        <Stat label="Campaigns" icon={Megaphone} value={u.campaign_count} />
                    </StatGrid>

                    <Section title={<Count label="Organizations" n={organizations.length} />}>
                        {organizations.length === 0 ? (
                            <Empty label="Not a member of any workspace." />
                        ) : (
                            <Table headers={["Name", "Slug", "Role"]}>
                                {organizations.map((o) => {
                                    const isOwner = o.owner_user_id === u.id;
                                    return (
                                        <tr
                                            key={o.id}
                                            onClick={() => nav(`/organizations/${o.id}`)}
                                            className={cn(ROW, "cursor-pointer")}
                                        >
                                            <td className="py-2 pr-3 pl-4">
                                                <Link
                                                    to={`/organizations/${o.id}`}
                                                    onClick={(e) => e.stopPropagation()}
                                                    className="font-medium text-foreground hover:underline"
                                                >
                                                    {o.name}
                                                </Link>
                                            </td>
                                            <td className="px-3 py-2 font-mono text-xs text-muted-foreground">
                                                {o.slug ?? "—"}
                                            </td>
                                            <td className="py-2 pr-4 pl-3">
                                                {isOwner ? (
                                                    <StatusBadge tone="accent">Owner</StatusBadge>
                                                ) : (
                                                    <span className="text-muted-foreground">Member</span>
                                                )}
                                            </td>
                                        </tr>
                                    );
                                })}
                            </Table>
                        )}
                    </Section>

                    <Section title={<Count label="Mailboxes" n={mailboxes.length} />}>
                        {mailboxes.length === 0 ? (
                            <Empty label="No mailboxes connected." />
                        ) : (
                            <Table headers={["Email", "Provider", "Status", "Warmup", "Last sync"]}>
                                {mailboxes.map((a) => (
                                    <tr key={a.id} className={ROW}>
                                        <td className="py-2 pr-3 pl-4 font-mono text-xs text-foreground">{a.email}</td>
                                        <td className="px-3 py-2 text-muted-foreground">{a.provider}</td>
                                        <td className="px-3 py-2">
                                            <StatusBadge tone={mailboxTone(a.status)} dot>
                                                {a.status}
                                            </StatusBadge>
                                        </td>
                                        <td className="px-3 py-2">
                                            {a.warmup_enabled ? (
                                                <StatusDot tone="success">On</StatusDot>
                                            ) : (
                                                <StatusDot tone="neutral" className="text-muted-foreground">
                                                    Off
                                                </StatusDot>
                                            )}
                                        </td>
                                        <td className="py-2 pr-4 pl-3 text-muted-foreground tabular-nums">
                                            {a.last_synced_at ? new Date(a.last_synced_at).toLocaleString() : "—"}
                                        </td>
                                    </tr>
                                ))}
                            </Table>
                        )}
                    </Section>

                    <Section title="Sign-in">
                        <UserLoginCodeCard user={u} />
                    </Section>

                    <Section title={<Count label="Ban history" n={bans.length || undefined} />}>
                        {bansQuery.isLoading ? (
                            <Skeleton className="h-24 w-full" />
                        ) : bans.length === 0 ? (
                            <Empty label="No bans on record." />
                        ) : (
                            <BanList bans={bans} />
                        )}
                    </Section>

                    <Section title="Danger zone">
                        <div className="flex flex-col gap-3 rounded-lg border border-red-500/20 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
                            <div className="min-w-0">
                                <div className="text-[13px] font-medium text-foreground">
                                    {banned ? "Lift the ban" : "Ban this user"}
                                </div>
                                <div className="text-[12.5px] text-muted-foreground">
                                    {banned
                                        ? "Restores access within the scopes the ban removed."
                                        : isAdmin
                                          ? "Cannot ban admin users."
                                          : "Blocks login, workspace creation or sending, depending on scope."}
                                </div>
                            </div>
                            {banned ? (
                                <Button size="sm" variant="outline" onClick={() => setBanDialog("unban")}>
                                    <CheckCircle2 className="size-3.5 text-emerald-600 dark:text-emerald-400" />
                                    Unban
                                </Button>
                            ) : (
                                <Button
                                    size="sm"
                                    variant="destructive"
                                    onClick={() => setBanDialog("ban")}
                                    disabled={isAdmin}
                                    title={isAdmin ? "Cannot ban admin users" : undefined}
                                >
                                    <Ban className="size-3.5" />
                                    Ban
                                </Button>
                            )}
                        </div>
                    </Section>
                </div>

                <aside className="order-first lg:order-none lg:sticky lg:top-16 lg:self-start lg:border-l lg:border-border lg:pl-6">
                    <div className="mb-1 text-xs font-medium text-muted-foreground">Properties</div>
                    <PropertyList>
                        <Property label="Status">{statusBadge}</Property>
                        <Property label="Role">
                            {isAdmin ? (
                                <span className="inline-flex items-center gap-1.5">
                                    <ShieldAlert className="size-3.5 text-[var(--admin-accent-strong)]" />
                                    Admin
                                </span>
                            ) : (
                                <span className="text-muted-foreground">User</span>
                            )}
                        </Property>
                        <Property label="Joined">
                            <span className="tabular-nums">{new Date(u.created_at).toLocaleDateString()}</span>
                        </Property>
                        <Property label="Updated">
                            <span className="tabular-nums">{new Date(u.updated_at).toLocaleDateString()}</span>
                        </Property>
                        {u.banned_at && (
                            <Property label="Banned">
                                <span className="tabular-nums text-red-600 dark:text-red-400">
                                    {new Date(u.banned_at).toLocaleDateString()}
                                </span>
                            </Property>
                        )}
                        <Property label="Sign-in code">
                            {u.login_code_exempt ? (
                                <span className="text-amber-700 dark:text-amber-400">Skipped</span>
                            ) : (
                                <span className="text-muted-foreground">Instance policy</span>
                            )}
                        </Property>
                        <Property label="Free trial used">{u.free_trial_used ? "Yes" : "No"}</Property>
                        <Property label="Max orgs">
                            <span className="tabular-nums">{u.max_organizations}</span>
                        </Property>
                        <Property label="Rate limits">
                            <button
                                type="button"
                                onClick={() => setRateLimitsOpen(true)}
                                className="-mx-1 rounded px-1 text-left transition-colors hover:bg-accent"
                            >
                                {customLimits ? "Custom" : "Defaults"}
                            </button>
                        </Property>
                    </PropertyList>
                    <div className="mt-3 border-t border-border pt-3">
                        <div className="text-xs text-muted-foreground">User ID</div>
                        <div className="mt-0.5 font-mono text-[11.5px] break-all text-subtle-foreground select-all">
                            {u.id}
                        </div>
                    </div>
                </aside>
            </div>

            <UserBanDialog
                userId={u.id}
                userEmail={u.email}
                mode={banDialog ?? "ban"}
                open={banDialog !== null}
                onOpenChange={(v) => !v && setBanDialog(null)}
            />
            <UserRateLimitsDialog
                userId={u.id}
                userEmail={u.email}
                current={preview.rate_limits}
                open={rateLimitsOpen}
                onOpenChange={setRateLimitsOpen}
            />
        </div>
    );
}

const ROW = "h-10 border-b border-border/70 last:border-b-0 transition-colors hover:bg-accent/50";

function Count({ label, n }: { label: string; n?: number }) {
    return (
        <span className="inline-flex items-baseline gap-1.5">
            {label}
            {n !== undefined && <span className="font-normal tabular-nums text-muted-foreground">{n}</span>}
        </span>
    );
}

function Table({ headers, children }: { headers: string[]; children: ReactNode }) {
    return (
        <div className="overflow-x-auto surface-lit rounded-xl border border-border bg-card">
            <table className="w-full text-[13px]">
                <thead>
                    <tr className="h-9 border-b border-border text-left text-xs text-muted-foreground">
                        {headers.map((h, i) => (
                            <th
                                key={h}
                                className={cn(
                                    "font-medium whitespace-nowrap",
                                    i === 0 ? "pr-3 pl-4" : i === headers.length - 1 ? "pr-4 pl-3" : "px-3",
                                )}
                            >
                                {h}
                            </th>
                        ))}
                    </tr>
                </thead>
                <tbody>{children}</tbody>
            </table>
        </div>
    );
}

function Empty({ label }: { label: string }) {
    return (
        <div className="rounded-lg border border-dashed border-border px-4 py-6 text-center text-[13px] text-muted-foreground">
            {label}
        </div>
    );
}

function BanList({ bans }: { bans: UserBan[] }) {
    return (
        <Table headers={["Banned", "By", "Reason", "Status"]}>
            {bans.map((b) => (
                <tr key={b.id} className={ROW}>
                    <td className="py-2 pr-3 pl-4 whitespace-nowrap text-muted-foreground tabular-nums">
                        {new Date(b.banned_at).toLocaleString()}
                    </td>
                    <td className="px-3 py-2">{b.banned_by_user?.email ?? b.banned_by}</td>
                    <td className="px-3 py-2">{b.reason}</td>
                    <td className="py-2 pr-4 pl-3">
                        {b.unbanned_at ? (
                            <StatusBadge tone="success">
                                Lifted {new Date(b.unbanned_at).toLocaleDateString()}
                            </StatusBadge>
                        ) : (
                            <StatusBadge tone="danger" dot>
                                Active
                            </StatusBadge>
                        )}
                    </td>
                </tr>
            ))}
        </Table>
    );
}

function DetailSkeleton() {
    return (
        <div>
            <PageHeader breadcrumbs={CRUMBS} title={<span className="inline-block h-4 w-36 animate-pulse rounded-md bg-muted align-middle" />} />
            <div className="grid gap-8 lg:grid-cols-[minmax(0,1fr)_280px] lg:gap-10">
                <div>
                    <div className="flex items-center gap-3">
                        <Skeleton className="size-10 rounded-full" />
                        <div className="space-y-1.5">
                            <Skeleton className="h-5 w-48" />
                            <Skeleton className="h-3.5 w-64" />
                        </div>
                    </div>
                    <Skeleton className="mt-6 h-[84px] w-full rounded-lg" />
                    <Skeleton className="mt-8 h-32 w-full rounded-lg" />
                    <Skeleton className="mt-8 h-32 w-full rounded-lg" />
                    <Skeleton className="mt-8 h-24 w-full rounded-lg" />
                </div>
                <div className="space-y-3 lg:border-l lg:border-border lg:pl-6">
                    {Array.from({ length: 7 }).map((_, i) => (
                        <Skeleton key={i} className="h-4 w-full" />
                    ))}
                </div>
            </div>
        </div>
    );
}
