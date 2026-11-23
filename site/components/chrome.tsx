import Link from "next/link";
import { API_URL, DASHBOARD_URL } from "@/lib/links";
import { ThemeToggle } from "@/components/theme-toggle";

const nav = [
  { href: "#product", label: "Product" },
  { href: "#ledger", label: "Ledger" },
  { href: "#flows", label: "How it works" },
  { href: "#developers", label: "Developers" },
  { href: "#numbers", label: "Numbers" },
];

export function Logo({ light }: { light?: boolean }) {
  return (
    <span className="flex items-center gap-2.5">
      <span className="relative h-4 w-4">
        <span className="absolute inset-0 rounded-full bg-pulse" />
        <span className="absolute inset-0 animate-ping rounded-full bg-pulse opacity-30 [animation-duration:2.4s]" />
      </span>
      <span className={`text-heading-sm ${light ? "text-white" : ""}`}>FinCore</span>
    </span>
  );
}

export function Header() {
  return (
    <header className="sticky top-0 z-40 border-b border-mist bg-white/90 shadow-nav backdrop-blur">
      <div className="mx-auto flex max-w-[1200px] items-center gap-8 px-4 pt-[11px] pb-3 md:px-6">
        <Link href="/" aria-label="FinCore home"><Logo /></Link>
        <nav className="hidden gap-3.5 lg:flex">
          {nav.map((n) => (
            <a key={n.href} href={n.href} className="relative px-2 py-2 text-label text-ink after:absolute after:inset-x-2 after:-bottom-px after:h-0.5 after:origin-left after:scale-x-0 after:bg-pulse after:transition-transform hover:after:scale-x-100">
              {n.label}
            </a>
          ))}
        </nav>
        <div className="ml-auto flex items-center gap-2">
          <ThemeToggle />
          <a href={DASHBOARD_URL} className="hidden rounded-pill border border-ink px-5 py-2.5 text-label transition-colors hover:bg-ink hover:text-white sm:inline-flex">Login</a>
          <a href={`${API_URL}/openapi.yaml`} className="inline-flex rounded-pill bg-ink px-5 py-2.5 text-label text-white transition-opacity hover:opacity-80">Get the API</a>
        </div>
      </div>
    </header>
  );
}

const footer = [
  { title: "Product", links: [["Payments", "#product"], ["Refunds", "#flows"], ["Payouts", "#flows"], ["Reconciliation", "#ledger"], ["Webhooks", "#developers"]] },
  { title: "Developers", links: [["API reference", `${API_URL}/openapi.yaml`], ["Admin dashboard", DASHBOARD_URL], ["Health", `${API_URL}/healthz`]] },
  { title: "Principles", links: [["Double-entry ledger", "#ledger"], ["Idempotency", "#developers"], ["Settlement reconciliation", "#flows"]] },
];

export function Footer() {
  return (
    <footer className="border-t border-mist">
      <div className="mx-auto grid max-w-[1200px] gap-12 px-4 py-16 md:grid-cols-[1.5fr_1fr_1fr_1fr] md:px-6">
        <div>
          <Logo />
          <p className="mt-6 max-w-xs text-label text-warm-gray">Ledger-backed payment processing. Every rupee is a balanced, immutable entry.</p>
        </div>
        {footer.map((c) => (
          <div key={c.title}>
            <h3 className="text-caption uppercase text-warm-gray">{c.title}</h3>
            <ul className="mt-4 space-y-2.5">
              {c.links.map(([label, href]) => (
                <li key={label}><a href={href} className="text-label underline decoration-transparent underline-offset-4 transition-colors hover:decoration-pulse">{label}</a></li>
              ))}
            </ul>
          </div>
        ))}
      </div>
      <div className="mx-auto flex max-w-[1200px] flex-wrap justify-between gap-4 border-t border-mist px-4 py-6 text-caption uppercase text-warm-gray md:px-6">
        <span>© FinCore · test mode</span>
        <span>Built with Go, PostgreSQL & Dragonfly</span>
      </div>
    </footer>
  );
}
