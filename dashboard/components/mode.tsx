import { LogOut } from "lucide-react";
import { logout, setMode } from "@/app/actions";
import { currentMode, liveConfigured } from "@/lib/mode";
import { getSession } from "@/lib/session";

/** Test | Live segmented switch, like Razorpay's. Live needs its own API configured. */
export async function ModeSwitch() {
  const [mode, live] = [await currentMode(), liveConfigured()];
  const seg = (m: "test" | "live", label: string) => {
    const active = mode === m;
    const disabled = m === "live" && !live;
    return (
      <button
        name="mode"
        value={m}
        disabled={disabled || active}
        title={disabled ? "Set FINCORE_LIVE_API_URL and FINCORE_LIVE_ADMIN_TOKEN to enable live mode" : `Switch to ${label.toLowerCase()} mode`}
        className={`inline-flex items-center gap-1.5 rounded-pill px-3 py-1 text-caption uppercase transition-colors ${
          active ? "bg-ink text-white" : disabled ? "cursor-not-allowed text-fog" : "text-slate hover:text-ink"
        }`}
      >
        {m === "test" && <span className={`h-1.5 w-1.5 rounded-full ${active ? "bg-pulse" : "bg-fog"}`} aria-hidden />}
        {label}
      </button>
    );
  };
  return (
    <form action={setMode} className="inline-flex rounded-pill border border-mist p-0.5" aria-label="Data mode">
      {seg("test", "Test")}
      {seg("live", "Live")}
    </form>
  );
}

/** Razorpay-style reminder that you are looking at test data. */
export async function ModeBanner() {
  if ((await currentMode()) === "live") return null;
  return (
    <div className="border-b border-pulse bg-white">
      <p className="px-4 py-1.5 text-center text-caption uppercase md:px-8">
        Test mode · sandbox data, no real money moves{liveConfigured() ? "" : " · live mode not configured"}
      </p>
    </div>
  );
}

export async function UserMenu() {
  const me = await getSession();
  if (!me) return null;
  const initials = me.name.split(/\s+/).map((w) => w[0]).join("").slice(0, 2).toUpperCase();
  return (
    <form action={logout} className="flex items-center gap-3 rounded-lg border border-mist bg-white p-2.5">
      <span className="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-ink text-caption text-white" aria-hidden>{initials}</span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-data" title={me.email}>{me.name}</span>
        <span className="block text-[11px] uppercase tracking-wider text-warm-gray">{me.role === "admin" ? "Admin" : "Read-only"}</span>
      </span>
      <button className="grid h-8 w-8 place-items-center rounded-lg text-slate transition-colors hover:bg-mist/50 hover:text-ink" title="Sign out" aria-label="Sign out">
        <LogOut size={15} strokeWidth={1.5} />
      </button>
    </form>
  );
}
