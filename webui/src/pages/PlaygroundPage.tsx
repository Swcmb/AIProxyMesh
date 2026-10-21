import { useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Play } from "lucide-react";
import { api } from "../lib/api";
import type { DebugInferenceResult, DebugModelsResponse, Protocol } from "../lib/types";
import { EmptyState, ErrorNotice, JsonBlock, Loading, PageHeader } from "../components/Common";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { Select } from "../components/ui/Select";
import { Textarea } from "../components/ui/Textarea";
import { formatApiErrorMessage } from "../lib/error-message";

export function PlaygroundPage() {
  const [protocol, setProtocol] = useState<Protocol>("chat");
  const [model, setModel] = useState("");
  const [prompt, setPrompt] = useState("用一句话介绍你自己。");
  const [keyMode, setKeyMode] = useState("auto");
  const [keyTier, setKeyTier] = useState("zen");
  const [keyID, setKeyID] = useState("");
  const models = useQuery({ queryKey: ["debug-models"], queryFn: () => api.get<DebugModelsResponse>("/api/debug/models") });
  const selectedKeys = useMemo(() => {
    return models.data?.keys?.[keyTier] ?? [];
  }, [models.data, keyTier]);

  const run = useMutation({
    mutationFn: () => {
      const request =
        protocol === "chat"
          ? { model, messages: [{ role: "user", content: prompt }], stream: false }
          : protocol === "responses"
            ? { model, input: prompt, stream: false }
            : { model, max_tokens: 1024, messages: [{ role: "user", content: prompt }] };
      return api.post<DebugInferenceResult>("/api/debug/inference", {
        protocol,
        key: keyMode === "selected" ? { mode: "selected", tier: keyTier, id: keyID } : { mode: "auto" },
        request,
      });
    },
  });

  if (models.isLoading && !models.data) {
    return <Loading label="读取模型与 Key…" />;
  }
  if (models.isError) {
    return <ErrorNotice message={formatApiErrorMessage(models.error)} />;
  }
  const result = run.data;
  const available = (models.data?.models ?? []).filter((item) => item.available_zen || item.available_go);

  return (
    <>
      <PageHeader eyebrow="Playground" title="请求调试台" description="以真实管理链路发起一次非流式推理；诊断请求不修改 Key/节点冷却状态。" />

      <div className="grid-2">
        <div className="card panel">
          <div className="panel-head">
            <h3>请求</h3>
            <span className="badge badge-neutral">非流式</span>
          </div>
          <div className="form-grid-tight">
            <label className="field-group">
              <span className="field-label">协议</span>
              <Select value={protocol} onChange={(event) => setProtocol(event.target.value as Protocol)}>
                <option value="chat">Chat Completions</option>
                <option value="responses">Responses</option>
                <option value="anthropic">Anthropic Messages</option>
              </Select>
            </label>
            <label className="field-group">
              <span className="field-label">模型</span>
              <Input list="model-options" value={model} onChange={(event) => setModel(event.target.value)} placeholder="deepseek-v4.1-flash" />
              <datalist id="model-options">
                {available.map((item) => (
                  <option key={item.model} value={item.model} />
                ))}
              </datalist>
            </label>
            <label className="field-group">
              <span className="field-label">Key 选择</span>
              <Select
                value={keyMode}
                onChange={(event) => {
                  setKeyMode(event.target.value);
                  if (event.target.value === "selected" && !keyID && selectedKeys[0]) {
                    setKeyID(selectedKeys[0].id);
                  }
                }}
              >
                <option value="auto">自动路由</option>
                <option value="selected">指定 Key</option>
              </Select>
            </label>
            {keyMode === "selected" ? (
              <>
                <label className="field-group">
                  <span className="field-label">Key Tier</span>
                  <Select
                    value={keyTier}
                    onChange={(event) => {
                      setKeyTier(event.target.value);
                      setKeyID("");
                    }}
                  >
                    <option value="zen">Zen</option>
                    <option value="go">Go</option>
                  </Select>
                </label>
                <label className="field-group">
                  <span className="field-label">Key</span>
                  <Select value={keyID} onChange={(event) => setKeyID(event.target.value)}>
                    <option value="">选择 Key</option>
                    {selectedKeys.map((item) => (
                      <option key={item.id} value={item.id}>
                        {item.display}（失败 {item.failures}）
                      </option>
                    ))}
                  </Select>
                </label>
              </>
            ) : null}
          </div>
          <label className="field-group">
            <span className="field-label">Prompt</span>
            <Textarea value={prompt} onChange={(event) => setPrompt(event.target.value)} rows={8} />
          </label>
          <div className="field-inline">
            <Button onClick={() => run.mutate()} disabled={run.isPending || !model.trim() || !prompt.trim() || (keyMode === "selected" && !keyID)}>
              <Play size={15} /> {run.isPending ? "请求中…" : "发送"}
            </Button>
            {run.isError ? <span className="field-error">{formatApiErrorMessage(run.error)}</span> : null}
          </div>
        </div>

        <div className="card panel">
          <div className="panel-head">
            <h3>结果</h3>
            {result ? (
              <span className={`badge ${result.ok ? "badge-success" : "badge-danger"}`}>
                HTTP {result.http_status} · {result.duration_ms} ms
              </span>
            ) : null}
          </div>
          {!result ? (
            <EmptyState title="尚未发送请求" hint="结果会显示路由、选中 Key 与上游响应" />
          ) : (
            <>
              <div className="timeline">
                <div className="timeline-item">
                  <span className="muted">模型 / 协议</span>
                  <span className="mono">{result.route.model} · {result.route.native_protocol}</span>
                  <span className="badge badge-info">{result.route.tier || "-"}</span>
                </div>
                <div className="timeline-item">
                  <span className="muted">渠道 / Key</span>
                  <span className="mono">{result.route.channel || "-"} · {result.route.key_id?.slice(0, 12) || "-"}</span>
                  <span className="badge badge-neutral">{result.route.attempts ?? 0} 次尝试</span>
                </div>
                {result.selected_key ? (
                  <div className="timeline-item">
                    <span className="muted">指定 Key 测试</span>
                    <span className="mono">{result.selected_key.display}</span>
                    <span className="badge badge-info">{result.key_test || "-"}</span>
                  </div>
                ) : null}
              </div>
              <JsonBlock value={result.response} />
            </>
          )}
        </div>
      </div>
    </>
  );
}
