const internalIdPattern = /(?:\s*·\s*)?(?:req|job)_[a-z0-9_-]+/gi;
export function sanitizeToastText(value?: string) {
  if (!value)
    return undefined;
  const sanitized = value.replace(internalIdPattern, "").replace(/\s{2,}/g, " ").trim();
  return sanitized || undefined;
}
