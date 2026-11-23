"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { ChevronRight, Menu, Search, X } from "lucide-react";
import { crumbs } from "@/lib/nav";
import { NavLinks } from "@/components/nav-links";

/** Breadcrumbs, global search (⌘K / Ctrl+K) and the mobile navigation drawer. */
export function Topbar({ sidebarFooter }: { sidebarFooter: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const input = useRef<HTMLInputElement>(null);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        input.current?.focus();
        input.current?.select();
      }
      if (e.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const trail = crumbs(pathname);
  return (
    <>
      <header className="sticky top-0 z-30 flex h-14 items-center gap-3 border-b border-mist bg-white/90 px-4 backdrop-blur md:px-8">
        <button type="button" onClick={() => setOpen(true)} className="grid h-9 w-9 place-items-center rounded-lg border border-mist lg:hidden" aria-label="Open navigation">
          <Menu size={16} strokeWidth={1.5} />
        </button>
        <nav aria-label="Breadcrumb" className="hidden min-w-0 items-center gap-1.5 text-data sm:flex">
          {trail.map((c, i) => (
            <span key={c.href} className="flex min-w-0 items-center gap-1.5">
              {i > 0 && <ChevronRight size={14} className="shrink-0 text-fog" aria-hidden />}
              {i === trail.length - 1 ? (
                <span className="truncate" aria-current="page">{c.label}</span>
              ) : (
                <Link href={c.href} className="truncate text-warm-gray hover:text-ink">{c.label}</Link>
              )}
            </span>
          ))}
        </nav>
        <form
          role="search"
          className="ml-auto w-full max-w-sm"
          onSubmit={(e) => {
            e.preventDefault();
            const q = input.current?.value.trim();
            if (q) router.push(`/search?q=${encodeURIComponent(q)}`);
          }}
        >
          <label className="flex items-center gap-2 rounded-lg border border-mist bg-white px-3 py-1.5 text-data focus-within:border-ink">
            <Search size={15} strokeWidth={1.5} className="text-slate" aria-hidden />
            <input ref={input} name="q" placeholder="Search payments, merchants, IDs" className="w-full bg-transparent outline-none placeholder:text-fog" aria-label="Search" />
            <kbd className="hidden rounded border border-mist px-1.5 text-[11px] text-slate sm:inline">⌘K</kbd>
          </label>
        </form>
      </header>

      {open && (
        <div className="fixed inset-0 z-50 lg:hidden" role="dialog" aria-modal="true" aria-label="Navigation">
          <button type="button" className="absolute inset-0 bg-ink/30" onClick={() => setOpen(false)} aria-label="Close navigation" />
          <aside className="absolute inset-y-0 left-0 flex w-72 flex-col border-r border-mist bg-sidebar p-4">
            <div className="mb-6 flex items-center justify-between px-3">
              <span className="flex items-center gap-2.5"><span className="h-3 w-3 rounded-full bg-pulse" aria-hidden /><span className="text-subheading">FinCore</span></span>
              <button type="button" onClick={() => setOpen(false)} aria-label="Close navigation" className="grid h-8 w-8 place-items-center rounded-lg border border-mist">
                <X size={15} />
              </button>
            </div>
            <div className="flex-1 overflow-y-auto"><NavLinks onNavigate={() => setOpen(false)} /></div>
            <div className="border-t border-mist pt-4">{sidebarFooter}</div>
          </aside>
        </div>
      )}
    </>
  );
}
