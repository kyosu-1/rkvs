import type { MetaFunction, LinksFunction } from "@remix-run/node";
import { Links, LiveReload, Meta, Outlet, ScrollRestoration } from "@remix-run/react";
import tailwindStyles from "./tailwind.css";

export const meta: MetaFunction = () => [
  { charset: "utf-8" },
  { title: "Raft Cluster Dashboard" },
  { viewport: "width=device-width,initial-scale=1" }
];

export const links: LinksFunction = () => [
  { rel: "stylesheet", href: tailwindStyles }
];

export default function Root() {
  return (
    <html lang="en" className="h-full bg-gray-50">
      <head>
        <Meta />
        <Links />
      </head>
      <body className="h-full">
        <Outlet />
        <ScrollRestoration />
        <LiveReload />
      </body>
    </html>
  );
}
