import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, Plus, RefreshCw, Save, ShieldCheck, Trash2 } from "lucide-react";
import { Link } from "react-router-dom";
import { api } from "../lib/api";
import type { ConfigView, Tier } from "../lib/types";
import { EmptyState, ErrorNotice, JsonBlock, Loading, PageHeader } from "../components/Common";
import { Button } from "../components/ui/Button";
import { ConfirmDialog } from "../components/ui/ConfirmDialog";
import { Drawer } from "../components/ui/Drawer";
import { Input } from "../components/ui/Input";
import { Modal } from "../components/ui/Modal";
import { Select } from "../components/ui/Select";
import { Switch } from "../components/ui/Switch";
import { Tabs } from "../components/ui/Tabs";
import { Textarea } from "../components/ui/Textarea";
import { useToast } from "../components/ui/Toast";
import { configUpdateFromView } from "../lib/config-update";
import { formatApiErrorMessage } from "../lib/error-message";

type SecretInputs = { server: string; zen: string; go: string };

const emptySecrets: SecretInputs = { server: "", zen: "", go: "" };

export function ConfigPage() {
  const queryClient = useQueryClient();
  const toast = useToast();
  const config = useQuery({ queryKey: ["config"], queryFn: () => api.get<ConfigView>("/api/config") });
  const [draft, setDraft] = useState<ConfigView | null>(null);
  const [savedSignature, setSavedSignature] = useState<string | null>(null);
  const [secrets, setSecrets] = useState<SecretInputs>(emptySecrets);
  const [revealed, setRevealed] = useState<unknown>(null);
  const [revealOpen, setRevealOpen] = useState(false);
  const [revealPassword, setRevealPassword] = useState("");
  const [revealDrawer, setRevealDrawer] = useState(false);
  const [conflictOpen, setConflictOpen] = useState(false);
  const [reloadConfirmOpen, setReloadConfirmOpen] = useState(false);
  const [account, setAccount] = useState({ current: "", username: "", password: "" });
  const [section, setSection] = useState("basic");

  useEffect(() => {
    if (config.data && !draft) {
      const clone = structuredClone(config.data);
      setDraft(clone);
      setSavedSignature(JSON.stringify(configUpdateFromView(clone)));
    }
  }, [config.data, draft]);

  const dirty = useMemo(() => {
    if (secrets.server.trim() || secrets.zen.trim() || secrets.go.trim()) {
      return true;
    }
    if (!draft) {
      return false;
    }
    return JSON.stringify(configUpdateFromView(draft)) !== savedSignature;
  }, [draft, savedSignature, secrets]);

  useEffect(() => {
    if (!dirty) {
      return;
    }
    const handler = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, [dirty]);

  const save = useMutation({
    mutationFn: async (force: boolean) => {
      if (!draft) throw new Error("配置尚未加载");
      // PUT /api/config 是全量替换：先确认远端基线未变化，避免覆盖其他会话的修改。
      if (!force && config.data) {
        const fresh = await api.get<ConfigView>("/api/config");
        const freshSignature = JSON.stringify(configUpdateFromView(fresh));
        const baselineSignature = JSON.stringify(configUpdateFromView(config.data));
        if (freshSignature !== baselineSignature) {
          throw new Error("CONFIG_CONFLICT");
        }
      }
      const payload = configUpdateFromView(draft);
      payload.server_keys = [
        ...draft.server_keys.map((item) => ({ id: item.id })),
        ...(secrets.server.trim() ? [{ value: secrets.server.trim() }] : []),
      ];
      payload.zen_keys = [
        ...draft.zen_keys.map((item) => ({ id: item.id })),
        ...(secrets.zen.trim() ? [{ value: secrets.zen.trim() }] : []),
      ];
      payload.go_keys = [
        ...draft.go_keys.map((item) => ({ id: item.id })),
        ...(secrets.go.trim() ? [{ value: secrets.go.trim() }] : []),
      ];
      // Legacy proxies are no longer edited here; keep whatever the loaded
      // configuration already holds so saving does not drop a migrated value.
      payload.proxies = draft.proxies.map((item) => ({ id: item.id }));
      return api.put("/api/config", payload);
    },
    onSuccess: () => {
      if (draft) {
        setSavedSignature(JSON.stringify(configUpdateFromView(draft)));
      }
      setDraft(null);
      setSecrets(emptySecrets);
      queryClient.invalidateQueries({ queryKey: ["config"] });
      queryClient.invalidateQueries({ queryKey: ["monitor"] });
      toast.success("配置已保存并通过校验");
    },
    onError: (error) => {
      if (error instanceof Error && error.message === "CONFIG_CONFLICT") {
        setConflictOpen(true);
      }
    },
  });

  const reload = useMutation({
    mutationFn: () => api.post("/api/config/reload"),
    onSuccess: () => {
      setDraft(null);
      queryClient.invalidateQueries({ queryKey: ["config"] });
      toast.success("已从磁盘重新加载配置");
    },
  });

  const reveal = useMutation({
    mutationFn: (password: string) => api.post<unknown>("/api/config/reveal", { password }),
    onSuccess: (data) => {
      setRevealed(data);
      setRevealOpen(false);
      setRevealPassword("");
      setRevealDrawer(true);
    },
  });

  const updateAccount = useMutation({
    mutationFn: () =>
      api.put("/api/account", {
        current_password: account.current,
        username: account.username,
        new_password: account.password,
      }),
    onSuccess: () => {
      setAccount({ current: "", username: "", password: "" });
      window.location.href = "/login";
    },
  });

  if (config.isLoading && !config.data) {
    return <Loading label="读取配置…" />;
  }
  if (config.isError) {
    return <ErrorNotice message={formatApiErrorMessage(config.error)} />;
  }
  if (!draft) {
    return <EmptyState title="配置不可用" />;
  }

  const patch = (change: (next: ConfigView) => void) => {
    const next = structuredClone(draft);
    change(next);
    setDraft(next);
  };

  const removeSecret = (kind: "server_keys" | "zen_keys" | "go_keys", id: string) => {
    patch((next) => {
      next[kind] = next[kind].filter((item) => item.id !== id);
    });
  };

  const requestReload = () => {
    if (dirty) {
      setReloadConfirmOpen(true);
    } else {
      reload.mutate();
    }
  };

  const egress = draft.egress;

  return (
    <>
      <PageHeader
        eyebrow="Configuration"
        title="系统设置"
        description="保存前会完整校验；失败时保留旧运行实例。热重载不中断进行中的请求。"
        actions={
          <>
            {dirty ? <span className="badge badge-warning">未保存更改</span> : null}
            <Button variant="secondary" onClick={requestReload} disabled={reload.isPending}>
              <RefreshCw size={15} /> 重新加载
            </Button>
            <Button onClick={() => save.mutate(false)} disabled={save.isPending || !dirty}>
              <Save size={15} /> {save.isPending ? "保存中…" : "保存配置"}
            </Button>
          </>
        }
      />
      {draft.restart_required_fields?.length ? (
        <ErrorNotice
          message={`保存后需要重启的字段：${draft.restart_required_fields.join(", ")}。保存成功不代表这些字段已生效。`}
        />
      ) : null}
      {save.isError && !(save.error instanceof Error && save.error.message === "CONFIG_CONFLICT") ? (
        <ErrorNotice message={formatApiErrorMessage(save.error)} />
      ) : null}
      {reload.isError ? <ErrorNotice message={formatApiErrorMessage(reload.error)} /> : null}

      <Tabs
        ariaLabel="设置分组"
        value={section}
        onChange={setSection}
        items={[
          { key: "basic", label: "基本" },
          { key: "secrets", label: "密钥" },
          { key: "egress", label: "出口调度" },
          { key: "runtime", label: "运行时" },
          { key: "admin", label: "管理端" },
        ]}
      />

      <div className="card panel" hidden={section !== "basic"}>
        <div className="panel-head">
          <h3>基本</h3>
          <span className="badge badge-neutral">core</span>
        </div>
        <div className="form-grid">
          <label className="field-group">
            <span className="field-label">监听地址</span>
            <Input value={draft.listen} onChange={(event) => patch((next) => { next.listen = event.target.value; })} />
          </label>
          <label className="field-group">
            <span className="field-label">优先层</span>
            <Select value={draft.prefer} onChange={(event) => patch((next) => { next.prefer = event.target.value as Tier; })}>
              <option value="zen">Zen</option>
              <option value="go">Go</option>
            </Select>
          </label>
          <label className="field-group">
            <span className="field-label">匿名通道</span>
            <div className="field-inline">
              <Switch
                checked={draft.anonymous}
                onChange={(event) => patch((next) => { next.anonymous = event.target.checked; })}
              />
              <span className="muted">使用 OpenCode public 凭证</span>
            </div>
          </label>
          <label className="field-group">
            <span className="field-label">最大尝试次数</span>
            <Input
              type="number"
              min={1}
              value={draft.retry.max_attempts}
              onChange={(event) => patch((next) => { next.retry.max_attempts = Number(event.target.value) || 1; })}
            />
          </label>
          <label className="field-group">
            <span className="field-label">请求超时（秒）</span>
            <Input
              type="number"
              min={1}
              value={draft.retry.timeout_seconds}
              onChange={(event) => patch((next) => { next.retry.timeout_seconds = Number(event.target.value) || 1; })}
            />
          </label>
        </div>
        <div className="form-grid">
          <label className="field-group">
            <span className="field-label">upstream.zen</span>
            <Input value={draft.upstream.zen} onChange={(event) => patch((next) => { next.upstream.zen = event.target.value; })} />
          </label>
          <label className="field-group">
            <span className="field-label">upstream.go</span>
            <Input value={draft.upstream.go} onChange={(event) => patch((next) => { next.upstream.go = event.target.value; })} />
          </label>
          <label className="field-group">
            <span className="field-label">upstream.anonymous</span>
            <Input
              value={draft.upstream.anonymous}
              onChange={(event) => patch((next) => { next.upstream.anonymous = event.target.value; })}
            />
          </label>
        </div>
      </div>

      <div className="card panel" hidden={section !== "secrets"}>
        <div className="panel-head">
          <h3>密钥</h3>
          <div className="field-inline">
            <span className="badge badge-neutral">默认掩码</span>
            <Button variant="secondary" size="sm" onClick={() => setRevealOpen(true)}>
              <Eye size={14} /> 显示完整密钥
            </Button>
          </div>
        </div>
        <SecretList title="本地 API Keys（server_keys）" items={draft.server_keys} onRemove={(id) => removeSecret("server_keys", id)} />
        <SecretList title="Zen Keys" items={draft.zen_keys} onRemove={(id) => removeSecret("zen_keys", id)} />
        <SecretList title="Go Keys" items={draft.go_keys} onRemove={(id) => removeSecret("go_keys", id)} />
        <div className="form-grid">
          <label className="field-group">
            <span className="field-label">新增本地 Key</span>
            <Input
              value={secrets.server}
              onChange={(event) => setSecrets({ ...secrets, server: event.target.value })}
              placeholder="留空不新增"
              autoComplete="off"
            />
          </label>
          <label className="field-group">
            <span className="field-label">新增 Zen Key</span>
            <Input
              value={secrets.zen}
              onChange={(event) => setSecrets({ ...secrets, zen: event.target.value })}
              placeholder="留空不新增"
              autoComplete="off"
            />
          </label>
          <label className="field-group">
            <span className="field-label">新增 Go Key</span>
            <Input
              value={secrets.go}
              onChange={(event) => setSecrets({ ...secrets, go: event.target.value })}
              placeholder="留空不新增"
              autoComplete="off"
            />
          </label>
        </div>
        {reveal.isError ? <ErrorNotice message={formatApiErrorMessage(reveal.error)} /> : null}
      </div>

      <div className="card panel" hidden={section !== "egress"}>
        <div className="panel-head">
          <h3>出口调度（egress）</h3>
          <span className={`badge ${egress?.enabled ? "badge-success" : "badge-muted"}`}>
            {egress?.enabled ? "已启用" : "未启用"}
          </span>
        </div>
        {!egress ? (
          <div className="field-inline">
            <Button
              variant="secondary"
              onClick={() =>
                patch((next) => {
                  next.egress = {
                    enabled: true,
                    import_legacy_proxies: true,
                    schedule: {},
                    protocols: {},
                    cooldown: {},
                    failover: {},
                    probe: {},
                  };
                })
              }
            >
              <Plus size={15} /> 启用出口池
            </Button>
          </div>
        ) : (
          <>
            <div className="form-grid">
              <label className="field-group">
                <span className="field-label">启用</span>
                <Switch
                  checked={egress.enabled}
                  onChange={(event) => patch((next) => { if (next.egress) next.egress.enabled = event.target.checked; })}
                />
              </label>
              <label className="field-group">
                <span className="field-label">导入旧代理</span>
                <Switch
                  checked={egress.import_legacy_proxies ?? true}
                  onChange={(event) =>
                    patch((next) => { if (next.egress) next.egress.import_legacy_proxies = event.target.checked; })
                  }
                />
              </label>
              <label className="field-group">
                <span className="field-label">冷却基数（秒）</span>
                <Input
                  type="number"
                  min={1}
                  value={egress.cooldown.base_seconds ?? 30}
                  onChange={(event) =>
                    patch((next) => { if (next.egress) next.egress.cooldown.base_seconds = Number(event.target.value) || 30; })
                  }
                />
              </label>
              <label className="field-group">
                <span className="field-label">冷却上限（秒）</span>
                <Input
                  type="number"
                  min={1}
                  value={egress.cooldown.max_seconds ?? 900}
                  onChange={(event) =>
                    patch((next) => { if (next.egress) next.egress.cooldown.max_seconds = Number(event.target.value) || 900; })
                  }
                />
              </label>
              <label className="field-group">
                <span className="field-label">遵循 Retry-After</span>
                <Switch
                  checked={egress.failover.honor_retry_after ?? true}
                  onChange={(event) =>
                    patch((next) => { if (next.egress) next.egress.failover.honor_retry_after = event.target.checked; })
                  }
                />
              </label>
              <label className="field-group">
                <span className="field-label">延迟表上限</span>
                <Input
                  type="number"
                  min={1}
                  value={egress.max_latency_entries ?? 128}
                  onChange={(event) =>
                    patch((next) => { if (next.egress) next.egress.max_latency_entries = Number(event.target.value) || 128; })
                  }
                />
              </label>
              <label className="field-group">
                <span className="field-label">探测间隔（秒）</span>
                <Input
                  type="number"
                  min={10}
                  value={egress.probe.interval_seconds ?? 60}
                  onChange={(event) =>
                    patch((next) => { if (next.egress) next.egress.probe.interval_seconds = Number(event.target.value) || 60; })
                  }
                />
              </label>
              <label className="field-group">
                <span className="field-label">探测超时（秒）</span>
                <Input
                  type="number"
                  min={1}
                  value={egress.probe.timeout_seconds ?? 10}
                  onChange={(event) =>
                    patch((next) => { if (next.egress) next.egress.probe.timeout_seconds = Number(event.target.value) || 10; })
                  }
                />
              </label>
              <label className="field-group">
                <span className="field-label">探测并发</span>
                <Input
                  type="number"
                  min={1}
                  value={egress.probe.concurrency ?? 8}
                  onChange={(event) =>
                    patch((next) => { if (next.egress) next.egress.probe.concurrency = Number(event.target.value) || 8; })
                  }
                />
              </label>
              <label className="field-group">
                <span className="field-label">探测目标（逗号分隔，留空从 upstream 派生）</span>
                <Input
                  value={(egress.probe.targets ?? []).join(",")}
                  onChange={(event) =>
                    patch((next) => {
                      if (next.egress) {
                        next.egress.probe.targets = event.target.value
                          .split(",")
                          .map((item) => item.trim())
                          .filter(Boolean);
                      }
                    })
                  }
                />
              </label>
            </div>

            <div className="panel-head">
              <h4>出口协议</h4>
              <span className="muted">Tier A</span>
            </div>
            <div className="field-inline">
              {(["http", "https", "socks5", "socks5h"] as const).map((name) => (
                <label className="field-inline" key={name}>
                  <Switch
                    checked={egress.protocols[name] ?? true}
                    onChange={(event) =>
                      patch((next) => {
                        if (next.egress) next.egress.protocols[name] = event.target.checked;
                      })
                    }
                  />
                  <span className="mono">{name}</span>
                </label>
              ))}
            </div>

            <div className="panel-head">
              <h4>评分权重</h4>
              <span className="muted">总和会自动归一化</span>
            </div>
            <div className="form-grid">
              {(
                [
                  ["health_weight", "健康 H", egress.schedule.health_weight ?? 0.3],
                  ["latency_weight", "延迟 L", egress.schedule.latency_weight ?? 0.15],
                  ["routing_weight", "负载 R", egress.schedule.routing_weight ?? 0.15],
                  ["upstream_success_weight", "成功率 U", egress.schedule.upstream_success_weight ?? 0.4],
                ] as const
              ).map(([key, label, value]) => (
                <label className="field-group" key={key}>
                  <span className="field-label">{label}</span>
                  <Input
                    type="number"
                    step="0.01"
                    min={0}
                    value={value}
                    onChange={(event) =>
                      patch((next) => {
                        if (next.egress) next.egress.schedule[key] = Number(event.target.value) || 0;
                      })
                    }
                  />
                </label>
              ))}
            </div>

            <p className="muted">
              订阅源管理请前往 <Link to="/subscriptions">代理订阅</Link>。当前 {egress.subscriptions?.length ?? 0} 个订阅。
            </p>
          </>
        )}
      </div>

      <div className="card panel" hidden={section !== "runtime"}>
        <div className="panel-head">
          <h3>性能、日志与模型</h3>
          <span className="badge badge-neutral">runtime</span>
        </div>
        <div className="form-grid">
          <label className="field-group">
            <span className="field-label">日志级别</span>
            <Select
              value={draft.logging.level}
              onChange={(event) => patch((next) => { next.logging.level = event.target.value; })}
            >
              <option value="debug">debug</option>
              <option value="info">info</option>
              <option value="warn">warn</option>
              <option value="error">error</option>
            </Select>
          </label>
          <label className="field-group">
            <span className="field-label">日志环大小</span>
            <Input
              type="number"
              value={draft.logging.ring_size}
              onChange={(event) => patch((next) => { next.logging.ring_size = Number(event.target.value) || 2000; })}
            />
          </label>
          <label className="field-group">
            <span className="field-label">转储请求体</span>
            <Switch
              checked={draft.logging.dump_request_bodies}
              onChange={(event) => patch((next) => { next.logging.dump_request_bodies = event.target.checked; })}
            />
          </label>
          <label className="field-group">
            <span className="field-label">模型刷新（秒）</span>
            <Input
              type="number"
              value={draft.models.refresh_seconds}
              onChange={(event) => patch((next) => { next.models.refresh_seconds = Number(event.target.value) || 300; })}
            />
          </label>
          <label className="field-group">
            <span className="field-label">仅免费模型</span>
            <Switch
              checked={draft.models.free_only}
              onChange={(event) => patch((next) => { next.models.free_only = event.target.checked; })}
            />
          </label>
          <label className="field-group">
            <span className="field-label">连接超时（秒）</span>
            <Input
              type="number"
              value={draft.performance.connect_timeout_seconds}
              onChange={(event) =>
                patch((next) => { next.performance.connect_timeout_seconds = Number(event.target.value) || 5; })
              }
            />
          </label>
          <label className="field-group">
            <span className="field-label">通道冷却（秒）</span>
            <Input
              type="number"
              value={draft.performance.failure_cooldown_seconds}
              onChange={(event) =>
                patch((next) => { next.performance.failure_cooldown_seconds = Number(event.target.value) || 15; })
              }
            />
          </label>
          <label className="field-group">
            <span className="field-label">尝试超时（秒）</span>
            <Input
              type="number"
              value={draft.performance.attempt_timeout_seconds}
              onChange={(event) =>
                patch((next) => { next.performance.attempt_timeout_seconds = Number(event.target.value) || 0; })
              }
            />
          </label>
        </div>
        <label className="field-group">
          <span className="field-label">强制推理强度（reasoning.effort）</span>
          <Input
            value={draft.reasoning.effort ?? ""}
            onChange={(event) => patch((next) => { next.reasoning.effort = event.target.value || undefined; })}
            placeholder="例如 high；留空表示不强制"
          />
        </label>
      </div>

      <div className="card panel" hidden={section !== "admin"}>
        <div className="panel-head">
          <h3>管理端与账号</h3>
          <ShieldCheck size={15} className="muted" />
        </div>
        <div className="form-grid">
          <label className="field-group">
            <span className="field-label">WebUI 启用</span>
            <Switch
              checked={draft.webui.enabled}
              onChange={(event) => patch((next) => { next.webui.enabled = event.target.checked; })}
            />
          </label>
          <label className="field-group">
            <span className="field-label">WebUI 监听</span>
            <Input
              value={draft.webui.listen}
              onChange={(event) => patch((next) => { next.webui.listen = event.target.value; })}
            />
          </label>
          <label className="field-group">
            <span className="field-label">会话时长（分钟）</span>
            <Input
              type="number"
              value={draft.webui.session_ttl_minutes}
              onChange={(event) =>
                patch((next) => { next.webui.session_ttl_minutes = Number(event.target.value) || 720; })
              }
            />
          </label>
        </div>
        <div className="form-grid">
          <label className="field-group">
            <span className="field-label">当前密码</span>
            <Input
              type="password"
              value={account.current}
              onChange={(event) => setAccount({ ...account, current: event.target.value })}
              autoComplete="current-password"
            />
          </label>
          <label className="field-group">
            <span className="field-label">新用户名（可选）</span>
            <Input
              value={account.username}
              onChange={(event) => setAccount({ ...account, username: event.target.value })}
              autoComplete="username"
            />
          </label>
          <label className="field-group">
            <span className="field-label">新密码（可选）</span>
            <Input
              type="password"
              value={account.password}
              onChange={(event) => setAccount({ ...account, password: event.target.value })}
              autoComplete="new-password"
            />
          </label>
          <div className="field-inline" style={{ alignItems: "flex-end" }}>
            <Button
              variant="secondary"
              disabled={!account.current || updateAccount.isPending}
              onClick={() => updateAccount.mutate()}
            >
              更新账号（需重新登录）
            </Button>
          </div>
        </div>
        {updateAccount.isError ? <ErrorNotice message={formatApiErrorMessage(updateAccount.error)} /> : null}
      </div>

      <div className="card panel" hidden={section !== "admin"}>
        <div className="panel-head">
          <h3>原始配置预览</h3>
          <span className="badge badge-neutral">只读</span>
        </div>
        <Textarea readOnly rows={10} value={JSON.stringify(configUpdateFromView(draft), null, 2)} />
      </div>

      <Modal
        open={revealOpen}
        onClose={() => {
          setRevealOpen(false);
          setRevealPassword("");
        }}
        title="验证身份"
        description="输入 WebUI 密码后显示完整密钥；结果仅在本次会话内展示。"
        size="sm"
        footer={
          <>
            <Button
              variant="secondary"
              onClick={() => {
                setRevealOpen(false);
                setRevealPassword("");
              }}
              disabled={reveal.isPending}
            >
              取消
            </Button>
            <Button
              onClick={() => reveal.mutate(revealPassword)}
              disabled={!revealPassword || reveal.isPending}
            >
              {reveal.isPending ? "验证中…" : "显示完整密钥"}
            </Button>
          </>
        }
      >
        <label className="field-group">
          <span className="field-label">WebUI 密码</span>
          <Input
            type="password"
            value={revealPassword}
            onChange={(event) => setRevealPassword(event.target.value)}
            autoComplete="current-password"
            autoFocus
            onKeyDown={(event) => {
              if (event.key === "Enter" && revealPassword) {
                reveal.mutate(revealPassword);
              }
            }}
          />
        </label>
      </Modal>

      <Drawer
        open={revealDrawer}
        onClose={() => {
          setRevealDrawer(false);
          setRevealed(null);
        }}
        title="完整密钥"
        subtitle="敏感内容：仅在必要时查看，请勿截图或分享。"
        size="lg"
      >
        {revealed ? <JsonBlock value={revealed} /> : null}
      </Drawer>

      <ConfirmDialog
        open={conflictOpen}
        title="远端配置已变化"
        description="其他会话已修改配置。继续保存会覆盖远端更改；取消后可先点击「重新加载」丢弃本地草稿并拉取最新配置。"
        confirmLabel="仍然保存"
        cancelLabel="取消"
        danger
        pending={save.isPending}
        onConfirm={() => {
          setConflictOpen(false);
          save.mutate(true);
        }}
        onCancel={() => setConflictOpen(false)}
      />

      <ConfirmDialog
        open={reloadConfirmOpen}
        title="放弃未保存更改"
        description="重新加载会用磁盘上的配置覆盖当前草稿，未保存的修改将丢失。"
        confirmLabel="重新加载"
        danger
        pending={reload.isPending}
        onConfirm={() => {
          setReloadConfirmOpen(false);
          reload.mutate();
        }}
        onCancel={() => setReloadConfirmOpen(false)}
      />
    </>
  );
}

function SecretList({
  title,
  items,
  onRemove,
}: {
  title: string;
  items: { id: string; display: string }[];
  onRemove: (id: string) => void;
}) {
  return (
    <div>
      <h4 className="panel-sub">{title}</h4>
      {items.length === 0 ? (
        <p className="muted">未配置</p>
      ) : (
        <div className="tag-list">
          {items.map((item) => (
            <span className="badge badge-neutral" key={item.id}>
              {item.display}
              <button className="icon-button" onClick={() => onRemove(item.id)} aria-label={`删除 ${item.display}`}>
                <Trash2 size={12} />
              </button>
            </span>
          ))}
        </div>
      )}
    </div>
  );
}
