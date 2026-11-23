import { Suspense } from "react";
import Link from "next/link";
import { ModeSwitch, UserMenu } from "@/components/mode";
import { NavLinks, NavTree } from "@/components/nav-links";
import { ThemeToggle } from "@/components/theme-toggle";

/** Footer shared by the desktop sidebar and the mobile drawer. */
export function SidebarFooter() {
  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-2 px-1">
        <Suspense fallback={<span className="h-7 w-28 rounded-pill border border-mist" />}>
          <ModeSwitch />
        </Suspense>
        <ThemeToggle />
      </div>
      <Suspense fallback={<div className="h-12 rounded-lg border border-mist" />}>
        <UserMenu />
      </Suspense>
    </div>
  );
}

/** Desktop sidebar. The shell prerenders; active state and the user stream in. */
export function Sidebar() {
  return (
    <aside className="sticky top-0 hidden h-screen w-64 shrink-0 flex-col border-r border-mist bg-sidebar px-4 py-5 lg:flex">
      <Link href="/" className="mb-8 flex items-center gap-2.5 px-3" aria-label="FinCore home">
        <span className="h-3.5 w-3.5 rounded-full bg-pulse" aria-hidden />
        <span className="text-heading-sm">FinCore</span>
        <span className="ml-auto rounded border border-mist px-1.5 text-[10px] uppercase tracking-wider text-slate">Console</span>
      </Link>
      <div className="flex-1 overflow-y-auto">
        <Suspense fallback={<NavTree pathname={null} />}>
          <NavLinks />
        </Suspense>
      </div>
      <div className="border-t border-mist pt-4">
        <SidebarFooter />
      </div>
    </aside>
  );
}
