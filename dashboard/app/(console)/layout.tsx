import { Suspense } from "react";
import { ModeBanner } from "@/components/mode";
import { Sidebar, SidebarFooter } from "@/components/nav";
import { Topbar } from "@/components/topbar";

export default function ConsoleLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-screen">
      <Sidebar />
      <div className="flex min-w-0 flex-1 flex-col">
        <Suspense fallback={null}>
          <ModeBanner />
        </Suspense>
        <Suspense fallback={<div className="sticky top-0 z-30 h-14 border-b border-mist bg-white/90" />}>
          <Topbar sidebarFooter={<SidebarFooter />} />
        </Suspense>
        <main className="mx-auto w-full max-w-[1280px] flex-1 px-4 pb-16 md:px-8">{children}</main>
      </div>
    </div>
  );
}
