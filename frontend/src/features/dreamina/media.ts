export interface MediaItem { id: string; type: "image" | "video"; url: string }
export interface HistoryResource { id: string; imageUrl: string }
export interface HistoryItem {
  id: string
  type: "image" | "video"
  prompt: string
  modelName: string
  resources: HistoryResource[]
  media: MediaItem[]
}

export function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

export function normalizeAssets(payload: Record<string, unknown>): MediaItem[] {
  return flattenHistoryMedia(normalizeHistoryItems(payload))
}

export function normalizeHistoryItems(payload: Record<string, unknown>): HistoryItem[] {
  const list = Array.isArray(payload.asset_list) ? payload.asset_list : []
  const history: HistoryItem[] = []
  list.forEach((rawItem, groupIndex) => {
    const item = asRecord(rawItem)
    const numericType = Number(item.type)
    const kind = numericType === 1 ? "image" : numericType === 2 || numericType === 10 ? "video" : null
    if (!kind) return
    const container = asRecord(item[kind])
    const itemList = Array.isArray(container.item_list) ? container.item_list : []
    const groupId = identifier(item.id, `asset-${groupIndex}`)
    const media: MediaItem[] = []
    itemList.forEach((rawMedia, index) => {
      const entry = asRecord(rawMedia)
      const nested = asRecord(entry[kind])
      const source = kind === "image"
        ? firstString(asRecordArray(nested.large_images), "image_url") || stringValue(nested.image_url) || stringValue(entry.image_url)
        : stringValue(asRecord(asRecord(nested.transcoded_video).origin).video_url) || stringValue(nested.video_url) || stringValue(entry.video_url)
      const safeSource = safeMediaURL(source)
      if (safeSource) media.push({ id: `${kind}-${groupId}-${index}`, type: kind, url: safeSource })
    })
    if (media.length === 0) return
    const resources = (Array.isArray(container.resources) ? container.resources : []).map((rawResource) => {
      const resource = asRecord(rawResource)
      const imageInfo = asRecord(resource.image_info)
      return {
        id: stringValue(imageInfo.image_uri) || identifier(resource.id, ""),
        imageUrl: safeMediaURL(stringValue(imageInfo.image_url) || stringValue(resource.image_url)),
      }
    }).filter((resource) => resource.id || resource.imageUrl)
    history.push({
      id: groupId,
      type: kind,
      prompt: normalizePrompt(stringValue(container.history_group_key)),
      modelName: stringValue(asRecord(container.model_info).model_name),
      resources,
      media,
    })
  })
  return history
}

export function flattenHistoryMedia(items: HistoryItem[]) {
  return items.flatMap((item) => item.media)
}

export function mergeHistoryItems(current: HistoryItem[], next: HistoryItem[]) {
  const seen = new Set<string>()
  return [...current, ...next].filter((item) => {
    const key = `${item.type}-${item.id}`
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}

export function mergeMedia(current: MediaItem[], next: MediaItem[]) {
  const seen = new Set(current.map((item) => item.id))
  return [...current, ...next.filter((item) => !seen.has(item.id))]
}

function asRecordArray(value: unknown): Record<string, unknown>[] { return Array.isArray(value) ? value.map(asRecord) : [] }
function firstString(items: Record<string, unknown>[], key: string) { return items.map((item) => stringValue(item[key])).find(Boolean) || "" }
function stringValue(value: unknown) { return typeof value === "string" ? value : "" }
function identifier(value: unknown, fallback: string) { return typeof value === "string" || typeof value === "number" ? String(value) : fallback }
function normalizePrompt(value: string) {
  const separator = value.indexOf("#")
  return (separator >= 0 ? value.slice(separator + 1) : value).trim()
}
function safeMediaURL(value: string) {
  try {
    const parsed = new URL(value)
    return parsed.protocol === "http:" || parsed.protocol === "https:" ? parsed.toString() : ""
  } catch {
    return ""
  }
}
