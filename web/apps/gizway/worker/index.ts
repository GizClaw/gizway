/** Cloudflare Worker entry point for the GizWay web prototype. */
import { handleImageOptimization, DEFAULT_DEVICE_SIZES, DEFAULT_IMAGE_SIZES } from "vinext/server/image-optimization";
import handler from "vinext/server/app-router-entry";

interface Fetcher {
  fetch(request: Request): Promise<Response>;
}

interface Env {
  ASSETS: Fetcher;
  IMAGES: {
    input(stream: ReadableStream): {
      transform(options: Record<string, unknown>): {
        output(options: { format: string; quality: number }): Promise<{ response(): Response }>;
      };
    };
  };
  GIZWAY_WEB_MODE?: string;
  GIZPAY_API_URL?: string;
  GIZPAY_POWERSYNC_URL?: string;
  GIZWAY_CN_API_URL?: string;
  GIZWAY_CN_POWERSYNC_URL?: string;
  GIZWAY_GLOBAL_API_URL?: string;
  GIZWAY_GLOBAL_POWERSYNC_URL?: string;
}

interface ExecutionContext {
  waitUntil(promise: Promise<unknown>): void;
  passThroughOnException(): void;
}

// Image security config. SVG sources with .svg extension auto-skip the
// optimization endpoint on the client side (served directly, no proxy).
// To route SVGs through the optimizer (with security headers), set
// dangerouslyAllowSVG: true in next.config.js and uncomment below:
// const imageConfig: ImageConfig = { dangerouslyAllowSVG: true };

const worker = {
  async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    const url = new URL(request.url);
    const cn = url.hostname.startsWith("cn.");
    const bindings = env ?? ({} as Env);
    const configured = (name: keyof Env): string | undefined => {
      const value = bindings[name];
      if (typeof value === "string" && value !== "") return value;
      const runtimeValue = typeof process !== "undefined" ? process.env[name] : undefined;
      return runtimeValue || undefined;
    };
    const wayAPI = cn ? configured("GIZWAY_CN_API_URL") : configured("GIZWAY_GLOBAL_API_URL");
    const waySync = cn ? configured("GIZWAY_CN_POWERSYNC_URL") : configured("GIZWAY_GLOBAL_POWERSYNC_URL");
    const gizPayAPI = configured("GIZPAY_API_URL");
    const gizPaySync = configured("GIZPAY_POWERSYNC_URL");

    if ((url.pathname === "/auth/runtime-config" || url.pathname === "/auth/catalog-token") && wayAPI) {
      return proxy(request, wayAPI, url.pathname);
    }
    if (url.pathname.startsWith("/_api/gizpay/") && gizPayAPI) {
      return proxy(request, gizPayAPI, url.pathname.slice("/_api/gizpay".length));
    }
    if (url.pathname.startsWith("/_api/gizway/") && wayAPI) {
      return proxy(request, wayAPI, url.pathname.slice("/_api/gizway".length));
    }
    if (url.pathname.startsWith("/_sync/gizpay") && gizPaySync) {
      return proxy(request, gizPaySync, url.pathname.slice("/_sync/gizpay".length) || "/");
    }
    if (url.pathname.startsWith("/_sync/gizway") && waySync) {
      return proxy(request, waySync, url.pathname.slice("/_sync/gizway".length) || "/");
    }

    if (url.pathname === "/_vinext/image") {
      const allowedWidths = [...DEFAULT_DEVICE_SIZES, ...DEFAULT_IMAGE_SIZES];
      return handleImageOptimization(request, {
        fetchAsset: (path) => bindings.ASSETS.fetch(new Request(new URL(path, request.url))),
        transformImage: async (body, { width, format, quality }) => {
          const result = await bindings.IMAGES.input(body).transform(width > 0 ? { width } : {}).output({ format, quality });
          return result.response();
        },
      }, allowedWidths);
    }

    return handler.fetch(request, env, ctx);
  },
};

function proxy(request: Request, baseURL: string, pathname: string): Promise<Response> {
  const source = new URL(request.url);
  const target = new URL(baseURL);
  target.pathname = `${target.pathname.replace(/\/$/, "")}${pathname.startsWith("/") ? pathname : `/${pathname}`}`;
  target.search = source.search;
  return fetch(new Request(target, request));
}

export default worker;
