import type { NextConfig } from "next";

const internalApiUrl = process.env.DEVPILOT_INTERNAL_API_URL ?? "http://127.0.0.1:8080";

const nextConfig: NextConfig = {
  outputFileTracingRoot: process.cwd(),
  poweredByHeader: false,
  reactStrictMode: true,
  async rewrites() {
    return [{ source: "/backend/:path*", destination: `${internalApiUrl}/:path*` }];
  },
};

export default nextConfig;
