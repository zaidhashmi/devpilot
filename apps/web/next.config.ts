import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  outputFileTracingRoot: process.cwd(),
  poweredByHeader: false,
  reactStrictMode: true,
  async rewrites() {
    return [{ source: "/backend/:path*", destination: "http://127.0.0.1:8080/:path*" }];
  },
};

export default nextConfig;
