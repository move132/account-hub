const accountStatusLabels: Record<string, string> = {
  enabled: "启用",
  disabled: "禁用",
};

const checkStateLabels: Record<string, string> = {
  unchecked: "未检查",
  valid: "有效",
  invalid: "无效",
  error: "异常",
};

const resultStatusLabels: Record<string, string> = {
  pending: "等待中",
  queued: "排队中",
  running: "执行中",
  succeeded: "成功",
  partially_succeeded: "部分成功",
  failed: "失败",
  cancelled: "已取消",
};

const operationLabels: Record<string, string> = {
  check: "检查",
  refresh: "刷新",
  credit: "查询积分",
  credit_claim: "领取积分",
  import: "导入",
  enable: "启用",
  disable: "禁用",
  delete: "删除",
};

const auditActionLabels: Record<string, string> = {
  login: "登录",
  login_failed: "登录失败",
  login_rate_limited: "登录限流",
  logout: "退出登录",
  password_change: "修改密码",
  token_create: "创建 Token",
  token_update: "更新 Token",
  token_delete: "删除 Token",
  token_check: "检查 Token",
  token_refresh: "刷新 Token",
  token_import: "导入 Token",
  token_export: "导出 Token",
  token_library_delete: "删除资料库图片",
  token_batch_check: "批量检查 Token",
  token_batch_refresh: "批量刷新 Token",
  token_batch_enable: "批量启用 Token",
  token_batch_disable: "批量禁用 Token",
  token_batch_delete: "批量删除 Token",
  dreamina_create: "创建即梦账号",
  dreamina_update: "更新即梦账号",
  dreamina_delete: "删除即梦账号",
  dreamina_check: "检查即梦账号",
  dreamina_refresh: "刷新即梦账号",
  dreamina_credit_claim: "领取即梦积分",
  dreamina_import: "导入即梦账号",
  dreamina_export: "导出即梦账号",
  dreamina_batch_check: "批量检查即梦账号",
  dreamina_batch_refresh: "批量刷新即梦账号",
  dreamina_batch_enable: "批量启用即梦账号",
  dreamina_batch_disable: "批量禁用即梦账号",
  dreamina_batch_delete: "批量删除即梦账号",
  job_cancel: "取消任务",
  job_delete: "删除任务",
  job_batch_delete: "批量删除任务",
  history_cleanup: "清理历史日志",
  settings_update: "更新系统设置",
};

const targetKindLabels: Record<string, string> = {
  auth: "认证",
  token: "Token",
  dreamina: "即梦账号",
  job: "任务",
  settings: "系统设置",
};

export function accountStatusLabel(value: string) {
  return accountStatusLabels[value] || value || "—";
}

export function checkStateLabel(value: string) {
  return checkStateLabels[value] || value || "—";
}

export function resultStatusLabel(value: string) {
  return resultStatusLabels[value] || value || "—";
}

export function operationLabel(value: string) {
  return operationLabels[value] || value || "—";
}

export function auditActionLabel(value: string) {
  return auditActionLabels[value] || value || "—";
}

export function targetKindLabel(value: string) {
  return targetKindLabels[value] || value || "—";
}

export function localizeStatusPreview(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map(localizeStatusPreview);
  }
  if (!value || typeof value !== "object") {
    return value;
  }
  return Object.fromEntries(Object.entries(value).map(([key, item]) => {
    if (typeof item === "string") {
      switch (key) {
        case "status":
          return [key, accountStatusLabels[item] || resultStatusLabels[item] || item];
        case "check_state":
          return [key, checkStateLabel(item)];
        case "state":
          return [key, resultStatusLabel(item)];
        case "operation":
          return [key, operationLabel(item)];
        case "action":
          return [key, auditActionLabel(item)];
        case "target_kind":
          return [key, targetKindLabel(item)];
      }
    }
    return [key, localizeStatusPreview(item)];
  }));
}
