import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Check, Copy } from "lucide-react";
import { api } from "../lib/api";
import type { ConfigView } from "../lib/types";
import { ErrorNotice, Loading, PageHeader } from "../components/Common";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { formatApiErrorMessage } from "../lib/error-message";

// deriveBase turns the gateway listen address into a browser-reachable origin.
// A wildcard or loopback listen is served from the same host the console runs
// on; only the port is taken from the config.
function deriveBase(listen: string): string {
  const port = listen.includes(":") ? listen.slice(listen.lastIndexOf(":") + 1) : "";
  const host = window.location.hostname || "127.0.0.1";
  return `${window.location.protocol}//${host}${port ? `:${port}` : ""}`;
}

export function AccessPage() {
  const config = useQuery({ queryKey: ["config"], queryFn: () => api.get<ConfigView>("/api/config") });
  const [model, setModel] = useState("");

  const base = useMemo(() => deriveBase(config.data?.listen ?? ""), [config.data?.listen]);
  const target = model.trim() || "YOUR_MODEL";
  const keyCount = config.data?.server_keys?.length ?? 0;

  if (config.isLoading && !config.data) {
    return <Loading label="读取接入信息…" />;
  }
  if (config.isError) {
    return <ErrorNotice message={formatApiErrorMessage(config.error)} />;
  }

  const examples = [
    {
      id: "curl-chat",
      label: "curl · Chat Completions",
      code: `curl ${base}/v1/chat/completions \\
  -H "Authorization: Bearer YOUR_MESH_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${target}",
    "messages": [{"role": "user", "content": "hello"}]
  }'`,
    },
    {
      id: "curl-responses",
      label: "curl · Responses",
      code: `curl ${base}/v1/responses \\
  -H "Authorization: Bearer YOUR_MESH_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${target}",
    "input": "hello"
  }'`,
    },
    {
      id: "openai-python",
      label: "OpenAI SDK · Python",
      code: `from openai import OpenAI

client = OpenAI(base_url="${base}/v1", api_key="YOUR_MESH_KEY")
response = client.chat.completions.create(
    model="${target}",
    messages=[{"role": "user", "content": "hello"}],
)
print(response.choices[0].message.content)`,
    },
    {
      id: "anthropic-python",
      label: "Anthropic SDK · Python",
      code: `from anthropic import Anthropic

client = Anthropic(base_url="${base}", api_key="YOUR_MESH_KEY")
message = client.messages.create(
    model="${target}",
    max_tokens=256,
    messages=[{"role": "user", "content": "hello"}],
)
print(message.content[0].text)`,
    },
    {
      id: "openai-js",
      label: "OpenAI SDK · TypeScript",
      code: `import OpenAI from "openai";

const client = new OpenAI({ baseURL: "${base}/v1", apiKey: "YOUR_MESH_KEY" });
const response = await client.chat.completions.create({
  model: "${target}",
  messages: [{ role: "user", content: "hello" }],
});
console.log(response.choices[0].message.content);`,
    },
  ];

  return (
    <>
      <PageHeader
        eyebrow="Access"
        title="接入手册"
        description="网关地址、鉴权方式与各 SDK 的接入示例；示例会跟随下方模型名变化。"
        actions={
          <div className="field-inline">
            <Input value={model} onChange={(event) => setModel(event.target.value)} placeholder={`模型 ID，如 ${target}`} />
          </div>
        }
      />

      <div className="card panel">
        <div className="panel-head">
          <h3>接入地址</h3>
          <span className="badge badge-neutral">{config.data?.listen}</span>
        </div>
        <dl className="access-list">
          <div>
            <dt>API Base</dt>
            <dd className="mono">{base}</dd>
          </div>
          <div>
            <dt>Chat Completions</dt>
            <dd className="mono">POST {base}/v1/chat/completions</dd>
          </div>
          <div>
            <dt>Responses</dt>
            <dd className="mono">POST {base}/v1/responses</dd>
          </div>
          <div>
            <dt>Anthropic Messages</dt>
            <dd className="mono">POST {base}/v1/messages</dd>
          </div>
          <div>
            <dt>模型列表</dt>
            <dd className="mono">GET {base}/v1/models</dd>
          </div>
          <div>
            <dt>鉴权</dt>
            <dd>
              <span className="mono">Authorization: Bearer &lt;server key&gt;</span>
              <div className="muted">
                {keyCount > 0 ? `当前配置了 ${keyCount} 个本地 Key，在「系统设置」查看或轮换。` : "尚未配置本地 Key，请先在「系统设置」中添加。"}
              </div>
            </dd>
          </div>
        </dl>
        <p className="muted">
          网关会按模型目录自动选择 Zen / Go 或匿名通道；客户端只需要一个模型 ID 和一个本地 Key。
        </p>
      </div>

      <div className="card panel">
        <div className="panel-head">
          <h3>示例</h3>
          <span className="badge badge-neutral">把 YOUR_MESH_KEY 换成本地 Key</span>
        </div>
        <div className="code-grid">
          {examples.map((example) => (
            <CodeCard key={example.id} label={example.label} code={example.code} />
          ))}
        </div>
      </div>
    </>
  );
}

function CodeCard({ label, code }: { label: string; code: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  };
  return (
    <div className="code-card">
      <div className="code-head">
        <span>{label}</span>
        <Button size="sm" variant="ghost" onClick={copy} aria-label="复制示例">
          {copied ? <Check size={13} /> : <Copy size={13} />} {copied ? "已复制" : "复制"}
        </Button>
      </div>
      <pre>
        <code>{code}</code>
      </pre>
    </div>
  );
}
