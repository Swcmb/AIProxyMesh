import type { ConfigView } from "./types";

export type SecretUpdate = { id?: string; value?: string };

export type ConfigUpdatePayload = {
  listen: string;
  server_keys: SecretUpdate[];
  zen_keys: SecretUpdate[];
  go_keys: SecretUpdate[];
  anonymous: boolean;
  proxies: SecretUpdate[];
  proxyfile: string;
  upstream: ConfigView["upstream"];
  retry: ConfigView["retry"];
  models: ConfigView["models"];
  performance: ConfigView["performance"];
  logging: ConfigView["logging"];
  prefer: ConfigView["prefer"];
  reasoning: ConfigView["reasoning"];
  egress?: ConfigView["egress"];
  webui: {
    enabled: boolean;
    listen: string;
    username: string;
    session_ttl_minutes: number;
  };
};

// The admin config endpoint replaces the whole section from one payload; every
// save must therefore carry the complete current configuration.
export function configUpdateFromView(
  view: ConfigView,
  egress: ConfigView["egress"] | null = view.egress,
): ConfigUpdatePayload {
  return {
    listen: view.listen,
    server_keys: view.server_keys.map((item) => ({ id: item.id })),
    zen_keys: view.zen_keys.map((item) => ({ id: item.id })),
    go_keys: view.go_keys.map((item) => ({ id: item.id })),
    anonymous: view.anonymous,
    proxies: view.proxies.map((item) => ({ id: item.id })),
    proxyfile: view.proxyfile,
    upstream: view.upstream,
    retry: view.retry,
    models: view.models,
    performance: view.performance,
    logging: view.logging,
    prefer: view.prefer,
    reasoning: view.reasoning,
    egress: egress ?? undefined,
    webui: {
      enabled: view.webui.enabled,
      listen: view.webui.listen,
      username: view.webui.username,
      session_ttl_minutes: view.webui.session_ttl_minutes,
    },
  };
}
