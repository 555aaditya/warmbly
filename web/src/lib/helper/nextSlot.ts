// A queued send time as a short label: "10:08" today, "tomorrow 09:12", or "Mon 09:12" later in the week.
export function nextSlotLabel(at: Date | string, now: Date = new Date()): string {
    const d = new Date(at);
    const time = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
    const day = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
    const days = Math.round((day(d) - day(now)) / 86_400_000);
    if (days <= 0) return time;
    if (days === 1) return `tomorrow ${time}`;
    if (days < 7) return `${d.toLocaleDateString([], { weekday: "short" })} ${time}`;
    return `${d.toLocaleDateString([], { month: "short", day: "numeric" })} ${time}`;
}
