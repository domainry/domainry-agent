import { useEffect, useRef, useState } from "react";
import { RunDialog } from "./RunDialog";
import { repairRequest } from "./execution-outcome.ts";
import type { ToolView } from "./execution-state.ts";
import { TodoDialog } from "./TodoDialog";
import { TaskDialog } from "./TaskDialog";
import { ScheduleDialog } from "./ScheduleDialog";
import { ArtifactDialog } from "./ArtifactDialog";
import { ToolSettingsDialog } from "./ToolSettingsDialog";
import { ExternalAccountsDialog } from "./ExternalAccountsDialog";
import { loadAuthorization } from "./external-account-state.ts";
import { KnowledgeLibraryDialog } from "./KnowledgeLibraryDialog";
import { AttachmentDialog } from "./AttachmentDialog";
import { ExecutionActivity } from "./ExecutionActivity";
import { KnowledgeResponse } from "./KnowledgeSources";
import { runCitations } from "./knowledge-state";
import { InteractionCard } from "./InteractionCard";
import { waiting, resumable } from "./interaction-state.ts";
import { liveStepText } from "./execution-state";
import {
  Archive,
  ArrowLeft,
  ArrowRight,
  Brain,
  Check,
  ChevronDown,
  Copy,
  FileText,
  Menu,
  MessageSquare,
  Plus,
  RotateCcw,
  Search,
  X,
  Sparkles,
  Trash2,
  LogOut,
  ArrowUpRight,
  ListChecks,
  Layers3,
  PenLine,
  MoreHorizontal,
  SlidersHorizontal,
  CalendarClock,
} from "lucide-react";
import {
  Conversation,
  ConversationContent,
  ConversationEmptyState,
  ConversationScrollButton,
} from "@/components/ai-elements/conversation";
import {
  Message,
  MessageContent,
} from "@/components/ai-elements/message";
import {
  PromptInput,
  PromptInputFooter,
  PromptInputTextarea,
  PromptInputSubmit,
  type PromptInputMessage,
} from "@/components/ai-elements/prompt-input";
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator } from "@/components/ui/dropdown-menu";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import {
  active,
  conversationPath,
  request,
  runPath,
  watchRun,
  type ConversationRecord,
  type ConversationPage,
  type MessagePage,
  type Run,
} from "./api";

import { DraftStore } from "./drafts";
import { describeError, errorMessage } from "./errors";
import { MemoryDialog, type MemorySource } from "./MemoryDialog";
import type { AppSession } from "./session";

const labels = {
  queued: "等待模型响应…",
  running: "正在处理",
  completed: "已保存",
  cancelled: "已停止",
  failed: "处理失败",
  waiting_user: "等待补充信息",
  waiting_confirmation: "等待操作确认",
  needs_reconciliation: "外部操作结果待核查",
};
export default function App({ session, onLogout, accountBusy, accountError }: { session: AppSession; onLogout?: () => void; accountBusy?: boolean; accountError?: string }) {
  const [toolSettings, setToolSettings] = useState(false);
  const [externalAccounts, setExternalAccounts] = useState(() => !!loadAuthorization(session.scope));
  const [narrow, setNarrow] = useState(() => window.matchMedia("(max-width: 650px)").matches);
  const [mobileNavigation, setMobileNavigation] = useState(false);
  const navigationButton = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    const media = window.matchMedia("(max-width: 650px)");
    const resize = () => { setNarrow(media.matches); if (!media.matches) setMobileNavigation(false); };
    resize();
    media.addEventListener("change", resize);
    return () => media.removeEventListener("change", resize);
  }, []);
  const [drafts] = useState(() => new DraftStore(() => localStorage, session.scope));
  const [selectedId, setSelectedId] = useState(location.hash.slice(1));
  const selectedRef = useRef(selectedId);
  selectedRef.current = selectedId;
  const [record, setRecord] = useState<ConversationRecord | null>(null);
  const [sessions, setSessions] = useState<ConversationPage>({ items: [] });
  const [cursors, setCursors] = useState([""]);
  const [archived, setArchived] = useState(false);
  const [history, setHistory] = useState<MessagePage>({ items: [] });
  const [historyCursors, setHistoryCursors] = useState([0]);
  const [run, setRun] = useState<Run | null>(null);
  const [refresh, setRefresh] = useState(0);
  const [input, setInputState] = useState(
    () => drafts.read(location.hash.slice(1)).text,
  );
  const [todoDialog, setTodoDialog] = useState(false);
  const [taskDialog, setTaskDialog] = useState(false);
  const [scheduleDialog, setScheduleDialog] = useState(false);
  const [artifactDialog, setArtifactDialog] = useState<false | true | { id: string; version: number }>(false);
  const [libraryDialog, setLibraryDialog] = useState(false);
  const [attachmentDialog, setAttachmentDialog] = useState(false);
  useEffect(() => setAttachmentDialog(false), [selectedId]);
  const [artifactWrite, setArtifactWrite] = useState(() => !!drafts.read(location.hash.slice(1)).artifactWrite);
  const [backgroundTaskWrite, setBackgroundTaskWrite] = useState(() => !!drafts.read(location.hash.slice(1)).backgroundTaskWrite);
  const [inspectedRun, setInspectedRun] = useState<{ conversationID: string; runID: string } | null>(null);
  const [todoWrite, setTodoWrite] = useState(() => !!drafts.read(location.hash.slice(1)).todoWrite);
  const [memoryWrite, setMemoryWrite] = useState(() => !!drafts.read(location.hash.slice(1)).memoryWrite);
  const [draftUnavailable, setDraftUnavailable] = useState(!drafts.available);
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [listLoading, setListLoading] = useState(true);
  const [listError, setListError] = useState("");
  const [notice, setNotice] = useState("");
  const [memoryDialog, setMemoryDialog] = useState<{
    source: MemorySource | null;
  } | null>(null);
  const [error, setError] = useState("");
  const [connectionError, setConnectionError] = useState("");
  const [busy, setBusy] = useState(false);
  const mutationLock = useRef(false);
  const [loading, setLoading] = useState(false);
  const status = session;
  const [dialog, setDialog] = useState<"new" | "rename" | "delete" | null>(
    null,
  );
  const [title, setTitle] = useState("");
  const [copied, setCopied] = useState("");
  const isActive = active(run);
  const isWaiting = waiting(run);
  const isObserved = isActive || isWaiting;
	const blocksConversation = isObserved && !run?.background_task;
  const canEdit =
    !!record && !record.active_run_id && !blocksConversation && !busy && !loading;
  const latestHistory = historyCursors.length === 1;
  const cursor = cursors[cursors.length - 1];

  const fail = (e: unknown) => setError(describeError(e));
  function refreshResultAccess() {
    // Account or tool changes can invalidate already rendered source content.
    // Clear it while the current conversation is reauthorized by the server.
    setHistory({ items: [] });
    setRun(null);
    setInspectedRun(null);
    setLoading(Boolean(selectedRef.current));
    setRefresh(value => value + 1);
  }
  function setInput(text: string) {
    drafts.write(selectedRef.current, text);
    setInputState(text);
    setDraftUnavailable(!drafts.available);
  }
  function resumeExecution(target: Run) {
    if (target.id !== run?.id || target.conversation_id !== selectedRef.current) return;
    void mutate(async () => {
      const resumed = await request<Run>(`${runPath(target.conversation_id, target.id)}/resume`, "POST");
      if (selectedRef.current === resumed.conversation_id) setRun(resumed);
      setRefresh(value => value + 1);
    });
  }
  const recoveryDisabled = busy || loading || isActive || !!record?.archived;
  const repairDisabled = !canEdit || !!record?.archived || !!input.trim() || !!drafts.read(selectedId).pending;
  function prepareRepair(target: Run, step: number, call: ToolView, label: string) {
    if (repairDisabled || target.conversation_id !== selectedRef.current) return;
    setInput(repairRequest(target, step, call, label));
    drafts.writeMemoryScope(selectedId, false); setMemoryWrite(false);
    drafts.writeTodoScope(selectedId, false); setTodoWrite(false);
    drafts.writeArtifactScope(selectedId, false); setArtifactWrite(false);
    drafts.writeBackgroundTaskScope(selectedId, false); setBackgroundTaskWrite(false);
    setInspectedRun(null);
    setNotice("修复请求已放入草稿，请核对后发送。原运行与已完成结果会保留。");
  }
  function activate(id: string, inspectRunID = "") {
    setMobileNavigation(false);
    selectedRef.current = id;
    location.hash = id;
    setSelectedId(id);
    setRecord(null);
    setRun(null);
    setHistory({ items: [] });
    setHistoryCursors([0]);
    setInputState(drafts.read(id).text);
    setMemoryWrite(!!drafts.read(id).memoryWrite);
    setTodoWrite(!!drafts.read(id).todoWrite);
    setArtifactWrite(!!drafts.read(id).artifactWrite);
    setBackgroundTaskWrite(!!drafts.read(id).backgroundTaskWrite);
    setDraftUnavailable(!drafts.available);
    setLoading(!!id);
    setError("");
    setConnectionError("");
    setNotice("");
    setInspectedRun(inspectRunID ? { conversationID: id, runID: inspectRunID } : null);
  }
  async function mutate(action: () => Promise<void>) {
    if (mutationLock.current) return;
    mutationLock.current = true;
    setBusy(true);
    setError("");
    try {
      await action();
    } catch (e) {
      fail(e);
    } finally {
      mutationLock.current = false;
      setBusy(false);
      if (location.hash.slice(1) !== selectedRef.current)
        activate(location.hash.slice(1));
    }
  }
  function select(id: string) {
    if (mutationLock.current) return;
    activate(id);
  }
  useEffect(() => {
    const handler = () => {
      if (location.hash.slice(1) !== selectedRef.current)
        select(location.hash.slice(1));
    };
    window.addEventListener("hashchange", handler);
    return () => window.removeEventListener("hashchange", handler);
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    setListLoading(true);
    setListError("");
    request<ConversationPage>(
      `/agent/conversations?limit=15&search=${encodeURIComponent(search)}&include_archived=${archived}&before_id=${encodeURIComponent(cursor)}`,
      "GET",
      undefined,
      controller.signal,
    )
      .then((page) => {
        if (!controller.signal.aborted) setSessions(page);
      })
      .catch((e) => {
        if (!controller.signal.aborted) {
          setListError(describeError(e));
          fail(e);
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setListLoading(false);
      });
    return () => controller.abort();
  }, [cursor, archived, search, refresh]);
  useEffect(() => {
    if (!selectedId) return;
    const controller = new AbortController();
    setLoading(true);
    (async () => {
      const [conversation, page] = await Promise.all([
        request<ConversationRecord>(
          conversationPath(selectedId),
          "GET",
          undefined,
          controller.signal,
        ),
        request<MessagePage>(
          `${conversationPath(selectedId)}/messages?limit=30&before_seq=${historyCursors.at(-1)}`,
          "GET",
          undefined,
          controller.signal,
        ),
      ]);
      const latestRunID =
        conversation.active_run_id ||
        (historyCursors.length === 1 ? page.items.at(-1)?.run_id : undefined);
      const snapshot = latestRunID
        ? await request<Run>(
            runPath(selectedId, latestRunID),
            "GET",
            undefined,
            controller.signal,
          )
        : null;
      if (!controller.signal.aborted) {
        setRecord(conversation);
        setHistory(page);
        setRun(snapshot);
      }
    })()
      .catch((e) => {
        if (!controller.signal.aborted) fail(e);
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [selectedId, historyCursors, refresh]);
  useEffect(() => {
    if (!run || !isObserved || run.conversation_id !== selectedId) return;
    return watchRun(
      run,
      (snapshot) => {
        setRun(snapshot);
        setConnectionError("");
      },
      () => setRefresh((v) => v + 1),
      (e) => setConnectionError(describeError(e)),
    );
    // Run snapshots change with every delta; the durable subscription changes
    // only when the run identity or active state changes.
  }, [selectedId, run?.id, isObserved]);

  async function update(patch: Partial<ConversationRecord>) {
    if (!record) return;
    const result = await request<ConversationRecord>(
      conversationPath(record.id),
      "PATCH",
      { ...patch, expected_revision: record.revision },
    );
    setRecord(result);
    setRefresh((v) => v + 1);
  }
  async function submit(message: PromptInputMessage) {
    const text = message.text.trim();
    if (!text || !record || record.active_run_id || blocksConversation || busy || record.archived) return;
    if (new TextEncoder().encode(text).length > 16384) {
      setError("消息超过 16 KB，请缩短后发送。");
      return;
    }
    await mutate(async () => {
      const pending = drafts.pending(record.id, text, memoryWrite, todoWrite, artifactWrite, backgroundTaskWrite);
      setDraftUnavailable(!drafts.available);
      const sent = await request<Run>(
        `${conversationPath(record.id)}/messages`,
        "POST",
        { client_message_id: pending.id, message: pending.text, ...(pending.memoryWrite || pending.todoWrite || pending.artifactWrite || pending.backgroundTaskWrite ? { write_scope: { personal_memory: !!pending.memoryWrite, ...(pending.todoWrite ? { personal_todos: true } : {}), ...(pending.artifactWrite ? { personal_artifacts: true } : {}), ...(pending.backgroundTaskWrite ? { background_tasks: true } : {}) } } : {}) },
      );
      drafts.acknowledge(record.id, pending);
      setInputState(drafts.read(record.id).text);
      setMemoryWrite(false);
      setTodoWrite(false);
      setArtifactWrite(false);
      setBackgroundTaskWrite(false);
      setRun(sent);
      setRecord({ ...record, active_run_id: sent.id });
      setHistoryCursors([0]);
      setRefresh((v) => v + 1);
    });
  }

  const allowedWrites = [
    (blocksConversation ? run?.write_scope?.personal_memory : memoryWrite) && "记忆",
    (blocksConversation ? run?.write_scope?.personal_todos : todoWrite) && "待办",
    (blocksConversation ? run?.write_scope?.personal_artifacts : artifactWrite) && "成果",
    (blocksConversation ? run?.write_scope?.background_tasks : backgroundTaskWrite) && "后台任务",
  ].filter(Boolean).join(" · ");
  const navigation = (
      <aside className="sidebar">
        <div className="brand">
          <span className="brand-symbol">d.</span>
          <div>
            Domainry<small>你的智能工作空间</small>
          </div>
        </div>
        <Button
          className="new-chat"
          onClick={() => {
            setMobileNavigation(false);
            setTitle("");
            setDialog("new");
          }}
          disabled={busy}
        >
          <Plus size={17} />
          新建会话
        </Button>
        <form
          role="search"
          aria-label="搜索会话"
          className="session-search"
          onSubmit={(event) => {
            event.preventDefault();
            setSearch(searchInput.trim());
            setCursors([""]);
          }}
        >
          <Input
            aria-label="搜索会话名称"
            placeholder="搜索会话名称…"
            value={searchInput}
            onChange={(event) => setSearchInput(event.target.value)}
            maxLength={160}
          />
          <Button
            type="submit"
            variant="ghost"
            size="icon-sm"
            aria-label="搜索会话"
          >
            <Search size={15} />
          </Button>
          {(search || searchInput) && (
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label="清除会话搜索"
              onClick={() => {
                setSearchInput("");
                setSearch("");
                setCursors([""]);
              }}
            >
              <X size={14} />
            </Button>
          )}
        </form>
        <div className="sidebar-caption">
          <span>我的会话</span>
          <label className="toggle-label">
            <Switch
              size="sm"
              aria-label="显示归档会话"
              checked={archived}
              onCheckedChange={(value) => {
                setArchived(value);
                setCursors([""]);
              }}
            />
            含归档
          </label>
        </div>
        <nav aria-label="会话列表" className="session-list">
          {listLoading ? (
            <p className="list-empty">正在读取会话…</p>
          ) : listError ? (
            <p className="list-empty">读取会话失败，请刷新重试。</p>
          ) : sessions.items.length ? (
            sessions.items.map((item) => (
              <button
                key={item.id}
                disabled={busy}
                aria-current={item.id === selectedId ? "page" : undefined}
                className={`session ${item.id === selectedId ? "selected" : ""}`}
                onClick={() => select(item.id)}
              >
                <MessageSquare size={16} />
                <span>
                  {item.title || "未命名会话"}
                  <small>
                    {item.archived
                      ? "已归档"
                      : item.active_run_id
                        ? run?.conversation_id === item.id ? labels[run.status] : "处理中"
                        : new Date(item.updated_at).toLocaleDateString(
                            "zh-CN",
                            { month: "long", day: "numeric" },
                          )}
                  </small>
                </span>
                {item.active_run_id && <span className="live-dot" />}
              </button>
            ))
          ) : (
            <p className="list-empty">
              {search
                ? "没有找到匹配的会话"
                : archived
                  ? "还没有会话"
                  : "从一个新会话开始"}
            </p>
          )}
        </nav>
        <div className="pagination">
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="上一页会话"
            disabled={cursors.length === 1 || listLoading || !!listError}
            onClick={() => setCursors((v) => v.slice(0, -1))}
          >
            <ArrowLeft size={14} />
          </Button>
          <span>第 {cursors.length} 页</span>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="下一页会话"
            disabled={!sessions.next_cursor || listLoading || !!listError}
            onClick={() => setCursors((v) => [...v, sessions.next_cursor!])}
          >
            <ArrowRight size={14} />
          </Button>
        </div>
        <div className="sidebar-bottom">
          <div className="library-caption">我的工作空间</div>
          <Button className="memory-link" variant="ghost" onClick={() => { setMobileNavigation(false); setTodoDialog(true); }}><Check size={17} />个人待办<span className="subtle">事项与截止日期</span></Button>
          <Button className="memory-link" variant="ghost" onClick={() => { setMobileNavigation(false); setTaskDialog(true); }}><ListChecks size={17} />后台任务<span className="subtle">进度、等待与成果</span></Button>
          {session.mode === "identity" && <Button className="memory-link" variant="ghost" onClick={() => { setMobileNavigation(false); setScheduleDialog(true); }}><CalendarClock size={17} />计划与提醒<span className="subtle">查看、修改与暂停</span></Button>}
          <Button className="memory-link" variant="ghost" onClick={() => { setMobileNavigation(false); setLibraryDialog(true); }}><FileText size={17} />资料库<span className="subtle">个人资料、共享成员</span></Button>
          <Button className="memory-link" variant="ghost" onClick={() => { setMobileNavigation(false); setArtifactDialog(true); }}><FileText size={17} />我的成果<span className="subtle">文档、表格与下载</span></Button>
          <Button
            variant="ghost"
            onClick={() => { setMobileNavigation(false); setMemoryDialog({ source: null }); }}
          >
            <Brain size={17} />
            个人记忆<span className="subtle">偏好与约定</span>
          </Button>
          {session.mode === "identity" && <Button className="memory-link" variant="ghost" onClick={() => { setMobileNavigation(false); setToolSettings(true); }}><SlidersHorizontal size={17} />工具设置<span className="subtle">管理个人工具开关</span></Button>}
          {session.mode === "identity" && <Button className="memory-link" variant="ghost" onClick={() => { setMobileNavigation(false); setExternalAccounts(true); }}><SlidersHorizontal size={17} />外部账号<span className="subtle">授权与连接管理</span></Button>}
          <div className="account-card">
            <span className="account-avatar">{Array.from(session.name || session.user_id || "D")[0].toUpperCase()}</span>
            <div><strong>{session.mode === "local" ? "本地工作空间" : session.name || session.user_id}</strong><small><span className="live-dot" />{session.mode === "local" ? "本地测试模式" : "个人账号"}</small></div>
            {onLogout && <Button variant="ghost" size="icon-sm" aria-label="退出登录" title="退出登录" disabled={accountBusy || busy} onClick={onLogout}><LogOut size={16} /></Button>}
          </div>
          {accountError && <p role="alert">{accountError}</p>}
        </div>
      </aside>
  );

  return (
    <div className="workspace">
      {narrow ? <Dialog open={mobileNavigation} onOpenChange={setMobileNavigation}>
        <DialogContent className="mobile-navigation" onCloseAutoFocus={event => {
          event.preventDefault();
          if (!todoDialog && !taskDialog && !artifactDialog && !memoryDialog && !dialog) navigationButton.current?.focus();
        }}>
          <DialogHeader className="mobile-navigation-heading"><DialogTitle>工作导航</DialogTitle><DialogDescription>选择会话，或管理待办、成果和个人记忆。</DialogDescription></DialogHeader>
          {navigation}
        </DialogContent>
      </Dialog> : navigation}
      <main className="chat-panel">
        <header className="chat-header">
          <div className="chat-heading">
            {narrow && <Button ref={navigationButton} variant="ghost" size="icon-sm" aria-label="打开工作导航" aria-haspopup="dialog" aria-expanded={mobileNavigation} onClick={() => setMobileNavigation(true)}><Menu size={20} /></Button>}
            <div>
            <div className="eyebrow">工作空间 <span>/</span> 对话</div>
            <h1>{record?.title || "开始新的工作"}</h1>
            </div>
          </div>
          <div className="header-actions">
            <Button variant="ghost" size="sm" disabled={!record || loading} onClick={() => setAttachmentDialog(true)}><FileText size={16} />附件</Button>
            <span className="model-chip">
              <span className={`live-dot ${status.ready ? "" : "offline"}`} />
              <span className="model-name" title={status.model}>{status.model || "工作助手"}</span><span className="model-status">{status.ready ? "已就绪" : "未连接"}</span>
            </span>
            <DropdownMenu>
              <DropdownMenuTrigger asChild><Button variant="ghost" size="icon" disabled={!canEdit} aria-label="会话操作" title="会话操作"><MoreHorizontal size={20} /></Button></DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="conversation-menu">
                <DropdownMenuItem disabled={!canEdit} onSelect={() => { setTitle(record!.title); setDialog("rename"); }}><PenLine size={16} />重命名</DropdownMenuItem>
                <DropdownMenuItem disabled={!canEdit} onSelect={() => void mutate(() => update({ archived: !record!.archived }))}><Archive size={16} />{record?.archived ? "恢复会话" : "归档会话"}</DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem variant="destructive" disabled={!canEdit} onSelect={() => setDialog("delete")}><Trash2 size={16} />删除会话</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </header>
        {(error || connectionError) && (
          <div role="alert" className="error-banner">
            <span>{error || connectionError}</span>
            <Button variant="ghost" size="sm" onClick={() => location.reload()}>
              刷新页面
            </Button>
          </div>
        )}
        <div className="chat-toolbar">
          <label className="toggle-label">
            <Switch
              aria-label="使用个人记忆"
              checked={record?.memory_enabled || false}
              disabled={!canEdit || record?.archived}
              onCheckedChange={(value) =>
                void mutate(() => update({ memory_enabled: value }))
              }
            />
            使用个人记忆
          </label>
          <span>
            {record?.summary_id
              ? "已自动整理早期上下文 · 原始消息仍保留"
              : record?.archived
                ? "会话已归档，恢复后可继续对话"
                : "历史记录与上下文会自动保存"}
          </span>
        </div>
        <Conversation
          key={`${selectedId}:${historyCursors.at(-1)}`}
          className="conversation-view"
          aria-label="聊天记录"
        >
          <ConversationContent className="conversation-content">
            {loading && !history.items.length ? (
              <p className="loading">正在读取会话…</p>
            ) : !history.items.length ? (
              <ConversationEmptyState className="welcome">
                <div className="welcome-intro"><span className="welcome-symbol"><Sparkles size={24} strokeWidth={1.5} /></span><span>让想法，向前一步</span></div>
                <h2>今天，<span>我们一起完成什么？</span></h2>
                <p>{record ? "从一个问题开始，让资料、思路和下一步逐渐清晰。" : "查找资料、梳理思路、推进工作。从这里开始。"}</p>
                <div className="suggestions" aria-label="开始一项工作">
                  {[
                    { icon: ListChecks, title: "安排今天的工作", description: "理清优先级，找到下一步", prompt: "帮我整理今天的工作计划，先问我三个关键问题。", conversation: "今天的工作", tone: "green" },
                    { icon: Search, title: "从文档中找答案", description: "检索相关资料，核对信息来源", prompt: "我想从知识库中查找资料，请先问我需要了解什么，再帮我检索并注明来源。", conversation: "文档检索", tone: "blue" },
                    { icon: Layers3, title: "梳理一个项目", description: "把目标拆成清晰的行动计划", prompt: "先帮我梳理项目的目标、约束和待办，问我三个问题。", conversation: "项目讨论", tone: "amber" },
                    { icon: PenLine, title: "打磨一份内容", description: "从初步想法，到成型的表达", prompt: "帮我起草一份工作文档，先确认读者、用途和我想表达的重点。", conversation: "内容创作", tone: "purple" },
                  ].map(item => <button key={item.title} type="button" className="suggestion-card" disabled={busy || loading || !!record?.archived || blocksConversation} onClick={() => {
                    setInput(item.prompt);
                    if (!record) { setTitle(item.conversation); setDialog("new"); }
                  }}><span className={`suggestion-icon ${item.tone}`}><item.icon size={20} strokeWidth={1.6} /></span><span className="suggestion-copy"><strong>{item.title}</strong><small>{item.description}</small></span><ArrowUpRight className="suggestion-arrow" size={16} /></button>)}
                </div>
                <div className="welcome-note"><span />每一次讨论，都可以接着往下走</div>
              </ConversationEmptyState>
            ) : (
              history.items.map((message) => (
                <Message
                  from={message.role}
                  key={message.id}
                  data-message-id={message.id}
                  data-background-task-id={message.background_task_id}
                >
                  <div className="message-name">
                    {message.role === "user" ? "你" : <><span className="message-avatar">d.</span>Domainry <span className="agent-label">Agent</span></>}
                    {message.background_task_id && <span className="agent-label">后台任务</span>}
                  </div>
                  <MessageContent>
                    {message.role === "assistant" && !message.access_error && run?.id === message.run_id && <ExecutionActivity run={run} onResume={() => resumeExecution(run)} onRepair={(step, call, label) => prepareRepair(run, step, call, label)} recoveryDisabled={recoveryDisabled} repairDisabled={repairDisabled} />}
                    {message.role === "assistant" ? (
                      message.access_error ? <p role="status" className="subtle">{errorMessage(message.access_error)}</p> : <KnowledgeResponse text={message.content} citations={message.citations} />
                    ) : (
                      <div className="user-text">{message.content}</div>
                    )}
                  </MessageContent>
                  <div className="message-actions">
                    {message.run_id && <Button variant="ghost" size="sm" onClick={() => setInspectedRun({ conversationID: selectedId, runID: message.run_id })}>查看处理记录</Button>}
                    {message.role === "assistant" && (
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label="复制回复"
                        className="copy-action"
                        onClick={() =>
                          void navigator.clipboard
                            .writeText(message.content)
                            .then(() => {
                              setCopied(message.id);
                              setTimeout(() => setCopied(""), 1600);
                            })
                            .catch(fail)
                        }
                      >
                        {copied === message.id ? (
                          <Check size={14} />
                        ) : (
                          <Copy size={14} />
                        )}
                      </Button>
                    )}
                    <Button
                      variant="ghost"
                      size="sm"
                      className="save-memory-action"
                      aria-label={
                        message.role === "user"
                          ? "将这条消息保存为记忆"
                          : "将这条回复保存为记忆"
                      }
                      onClick={() =>
                        setMemoryDialog({
                          source: {
                            title: "对话约定",
                            content: message.content,
                          },
                        })
                      }
                    >
                      <Brain size={14} />
                      保存为记忆
                    </Button>
                  </div>
                </Message>
              ))
            )}
            {latestHistory && run && run.status !== "completed" && (
              <Message from="assistant" data-run-status={run.status}>
                <div className="message-name">
                  <span className="message-avatar">d.</span>Domainry <span className="agent-label">Agent</span>{" "}
                  {run.background_task && <span className="agent-label">后台任务</span>}
                  {run.attempt > 1 && <span>· 第 {run.attempt} 次处理</span>}
                </div>
                <MessageContent>
                  <ExecutionActivity run={run} onResume={() => resumeExecution(run)} onRepair={(step, call, label) => prepareRepair(run, step, call, label)} recoveryDisabled={recoveryDisabled} repairDisabled={repairDisabled} />
                  {!run.access_error && run.interaction && <InteractionCard key={`${run.interaction.id}:${run.interaction.revision}`} interaction={run.interaction} onRun={snapshot => { if (selectedRef.current === snapshot.conversation_id) setRun(snapshot); }} onRefresh={() => setRefresh(value => value + 1)} />}
                  {run.access_error ? <p role="alert" className="subtle">{errorMessage(run.access_error)}</p> : liveStepText(run) ? (
                    <KnowledgeResponse isAnimating={isActive} text={liveStepText(run)} citations={runCitations(run)} />
                  ) : (
                    <span className={isActive ? "thinking" : "subtle"}>
                      {isActive ? "正在思考…" : isWaiting ? "完成上方操作后继续处理。" : "本次没有生成完整回复"}
                    </span>
                  )}
                </MessageContent>
                {!isActive && (
                  <small className="subtle">
                    {labels[run.status]}
                    {run.error_code && ` · ${errorMessage(run.error_code)}`}
                  </small>
                )}
              </Message>
            )}
          </ConversationContent>
          <ConversationScrollButton aria-label="滚动到最新消息" />
        </Conversation>
        {(history.next_before_seq || !latestHistory) && <div className="history-navigation">
          <Button
            variant="ghost"
            size="sm"
            disabled={!history.next_before_seq || loading || blocksConversation}
            onClick={() =>
              setHistoryCursors((v) => [...v, history.next_before_seq!])
            }
          >
            更早消息
          </Button>
          <span>
            {latestHistory ? "最新消息" : `历史第 ${historyCursors.length} 页`}
          </span>
          <Button
            variant="ghost"
            size="sm"
            disabled={latestHistory || loading}
            onClick={() => setHistoryCursors((v) => v.slice(0, -1))}
          >
            更新消息
            <ChevronDown size={13} />
          </Button>
        </div>}
        <div className="composer-wrap">
          <div role="status" aria-live="polite" className="run-status">
            {run ? labels[run.status] : record ? "准备就绪" : "请先新建会话"}
            {run?.status === "failed" &&
              run.error_code &&
              ` · ${errorMessage(run.error_code)}`}
          </div>
          {notice && (
            <p className="composer-notice" role="status">
              {notice}
            </p>
          )}
          {!!drafts.read(selectedId).pending && <p className="composer-notice">上次发送的结果尚未确认；重试相同消息会保留当时的操作授权范围。</p>}
          <details className="scope-settings" key={selectedId} onBlur={event => {
            if (!event.currentTarget.contains(event.relatedTarget)) event.currentTarget.open = false;
          }} onKeyDown={event => {
            if (event.key === "Escape") {
              event.currentTarget.open = false;
              event.currentTarget.querySelector("summary")?.focus();
              event.stopPropagation();
            }
          }}>
            <summary><SlidersHorizontal size={14} /><span>本次操作权限</span><span className="scope-summary">{allowedWrites ? `允许：${allowedWrites}` : "未授权写入"}</span><ChevronDown size={13} /></summary>
            <div className="scope-options">
              <p className="scope-description">仅对本次请求生效，请按需要选择允许修改的内容。</p>
          {blocksConversation && run?.write_scope?.personal_memory && <p className="composer-notice">本次处理已获准创建、修改或删除你的个人记忆。</p>}
          {!blocksConversation && <label className="memory-write-scope">
            <input type="checkbox" checked={memoryWrite} disabled={!canEdit || record?.archived || !!drafts.read(selectedId).pending} onChange={event => {
              const allowed = event.target.checked;
              drafts.writeMemoryScope(selectedId, allowed); setMemoryWrite(allowed); setDraftUnavailable(!drafts.available);
            }} />
            允许本次请求创建、修改或删除我的个人记忆
          </label>}
          {blocksConversation && run?.write_scope?.personal_todos && <p className="composer-notice">本次处理已获准创建、修改或删除你的个人待办。</p>}
          {!blocksConversation && <label className="memory-write-scope"><input type="checkbox" checked={todoWrite} disabled={!canEdit || record?.archived || !!drafts.read(selectedId).pending} onChange={event => { const allowed = event.target.checked; drafts.writeTodoScope(selectedId, allowed); setTodoWrite(allowed); setDraftUnavailable(!drafts.available); }} />允许本次请求创建、修改或删除我的个人待办</label>}
          {blocksConversation && run?.write_scope?.personal_artifacts && <p className="composer-notice">本次处理已获准创建、修改或导出你的成果。</p>}
          {!blocksConversation && <label className="memory-write-scope"><input type="checkbox" checked={artifactWrite} disabled={!canEdit || record?.archived || !!drafts.read(selectedId).pending} onChange={event => { const allowed = event.target.checked; drafts.writeArtifactScope(selectedId, allowed); setArtifactWrite(allowed); setDraftUnavailable(!drafts.available); }} />允许本次请求创建、修改或导出我的成果</label>}
          {blocksConversation && run?.write_scope?.background_tasks && <p className="composer-notice">本次处理已获准创建独立的后台任务。</p>}
          {!blocksConversation && <label className="memory-write-scope"><input type="checkbox" checked={backgroundTaskWrite} disabled={!canEdit || record?.archived || !!drafts.read(selectedId).pending} onChange={event => { const allowed = event.target.checked; drafts.writeBackgroundTaskScope(selectedId, allowed); setBackgroundTaskWrite(allowed); setDraftUnavailable(!drafts.available); }} />允许本次请求创建独立的后台任务</label>}
            </div>
          </details>
          <PromptInput onSubmit={submit} className="composer">
            <PromptInputTextarea
              aria-label="消息"
              placeholder={
                record?.archived
                  ? "恢复会话后继续讨论…"
                  : blocksConversation && isWaiting ? "请先处理上方的补充信息或确认事项…"
                  : "输入消息，继续你的讨论…"
              }
              value={input}
              onChange={(e) => setInput(e.currentTarget.value)}
              disabled={
                !record || record.archived || busy || loading || !!record.active_run_id || blocksConversation
              }
            />
            <PromptInputFooter>
              <span className="input-hint">
                {draftUnavailable
                  ? "浏览器存储不可用，草稿仅保留在当前页面"
                  : input
                    ? "草稿已保存在此浏览器 · Enter 发送"
                    : "Enter 发送 · Shift + Enter 换行"}
              </span>
              <div className="composer-actions">
                {run &&
                  !isActive &&
                  resumable(run) && !isWaiting && (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      disabled={!canEdit || record?.archived}
                      onClick={() =>
                        void mutate(async () => {
                          const resumed = await request<Run>(
                            `${runPath(selectedId, run.id)}/resume`,
                            "POST",
                          );
                          setRun(resumed);
                          setRefresh((v) => v + 1);
                        })
                      }
                    >
                      <RotateCcw size={14} />
                      {run.steps?.length ? "继续处理" : "重新生成"}
                    </Button>
                  )}
                <PromptInputSubmit
                  status={isActive && !run?.background_task ? "streaming" : "ready"}
                  aria-label={isActive && !run?.background_task ? "停止生成" : "发送消息"}
                  disabled={
                    busy ||
                    (!(isActive && !run?.background_task) &&
                      (!input.trim() || !record || record.archived || !!record.active_run_id || blocksConversation || loading))
                  }
                  onStop={() =>
                    void mutate(async () => {
                      if (!run) return;
                      const cancelled = await request<Run>(
                        `${runPath(selectedId, run.id)}/cancel`,
                        "POST",
                      );
                      setRun(cancelled);
                      setRefresh((v) => v + 1);
                    })
                  }
                />
              </div>
            </PromptInputFooter>
          </PromptInput>
          <p className="footnote">
            对话与执行记录会自动保存，请核对重要结果。
          </p>
        </div>
      </main>
      <Dialog
        open={dialog === "new" || dialog === "rename"}
        onOpenChange={(open) => {
          if (!open && !busy) setDialog(null);
        }}
      >
        <DialogContent>
          {error && (
            <p role="alert" className="text-destructive text-sm">
              {error}
            </p>
          )}
          <DialogHeader>
            <DialogTitle>
              {dialog === "new" ? "新建会话" : "重命名会话"}
            </DialogTitle>
            <DialogDescription>
              给这次讨论取一个容易找到的名字。
            </DialogDescription>
          </DialogHeader>
          <form
            className="dialog-form"
            onSubmit={(e) => {
              e.preventDefault();
              void mutate(async () => {
                if (dialog === "new") {
                  const created = await request<ConversationRecord>(
                    "/agent/conversations",
                    "POST",
                    {
                      client_id: crypto.randomUUID(),
                      title: title.trim(),
                      memory_enabled: false,
                    },
                  );
                  if (!selectedRef.current) {
                    drafts.write(created.id, drafts.read("").text);
                    drafts.remove("");
                  }
                  activate(created.id);
                  setRecord(created);
                  setSearch("");
                  setSearchInput("");
                  setCursors([""]);
                } else await update({ title: title.trim() });
                setDialog(null);
                setRefresh((v) => v + 1);
              });
            }}
          >
            <label htmlFor="session-title">会话名称</label>
            <Input
              id="session-title"
              value={title}
              maxLength={80}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="例如：项目讨论"
              required
            />
            <Button disabled={busy || !title.trim()} type="submit">
              保存
            </Button>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog
        open={dialog === "delete"}
        onOpenChange={(open) => {
          if (!open && !busy) setDialog(null);
        }}
      >
        <DialogContent>
          {error && (
            <p role="alert" className="text-destructive text-sm">
              {error}
            </p>
          )}
          <DialogHeader>
            <DialogTitle>删除这个会话？</DialogTitle>
            <DialogDescription>
              会话中的消息、草稿和摘要会一起删除。
            </DialogDescription>
          </DialogHeader>
          <Button
            variant="destructive"
            disabled={busy}
            onClick={() =>
              void mutate(async () => {
                await request(
                  `${conversationPath(selectedId)}?expected_revision=${record!.revision}`,
                  "DELETE",
                );
                drafts.remove(selectedId);
                activate("");
                setDialog(null);
                setRefresh((v) => v + 1);
              })
            }
          >
            确认删除
          </Button>
        </DialogContent>
      </Dialog>
      {inspectedRun && inspectedRun.conversationID === selectedId && <RunDialog key={`${inspectedRun.conversationID}:${inspectedRun.runID}`} {...inspectedRun} onClose={() => setInspectedRun(null)} onResume={inspectedRun.runID === run?.id ? resumeExecution : undefined} onRepair={prepareRepair} recoveryDisabled={recoveryDisabled} repairDisabled={repairDisabled} />}
      {todoDialog && <TodoDialog conversationID={selectedId} onClose={() => setTodoDialog(false)} onSource={id => { setTodoDialog(false); activate(id); }} />}
      {taskDialog && <TaskDialog conversationID={selectedId} onClose={() => setTaskDialog(false)} onSource={id => { setTaskDialog(false); activate(id); }} onRun={(conversationID, runID) => { setTaskDialog(false); activate(conversationID, runID); }} onArtifact={(id, version) => { setTaskDialog(false); setArtifactDialog({ id, version }); }} />}
      {scheduleDialog && <ScheduleDialog onClose={() => setScheduleDialog(false)} onAsk={prompt => { setScheduleDialog(false); setInputState(prompt); }} />}
      {toolSettings && <ToolSettingsDialog key={session.scope} session={session} onClose={() => { setToolSettings(false); refreshResultAccess(); }} />}
      {externalAccounts && <ExternalAccountsDialog session={session} onClose={() => { setExternalAccounts(false); refreshResultAccess(); }} />}
      {libraryDialog && <KnowledgeLibraryDialog onClose={() => setLibraryDialog(false)} />}
      {artifactDialog && <ArtifactDialog conversationID={selectedId} initial={typeof artifactDialog === "object" ? artifactDialog : undefined} onClose={() => setArtifactDialog(false)} onSource={conversationID => { setArtifactDialog(false); activate(conversationID); }} onRun={(conversationID, runID) => { setArtifactDialog(false); activate(conversationID, runID); }} />}
      {attachmentDialog && record && <AttachmentDialog key={record.id} conversationID={record.id} archived={record.archived} onClose={() => setAttachmentDialog(false)} onOpenLibrary={() => { setAttachmentDialog(false); setLibraryDialog(true); }} />}
      {memoryDialog && (
        <MemoryDialog
          source={memoryDialog.source}
          onClose={() => setMemoryDialog(null)}
          onSaved={() =>
            setNotice(
              "个人记忆已保存；开启「使用个人记忆」的会话会在后续对话中使用。",
            )
          }
        />
      )}
    </div>
  );
}
