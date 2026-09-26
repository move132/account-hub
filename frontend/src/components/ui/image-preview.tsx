import ChevronLeft from "lucide-react/dist/esm/icons/chevron-left.mjs";
import ChevronRight from "lucide-react/dist/esm/icons/chevron-right.mjs";
import X from "lucide-react/dist/esm/icons/x.mjs";
import { createPortal } from "react-dom";
import { Button, cn } from "./primitives";
import { Spinner } from "./spinner";

export interface ImagePreviewItem {
  url: string;
  label: string;
}

export function ImagePreview({
  image,
  index = 0,
  total = 1,
  loading = false,
  error = "",
  onPrevious,
  onNext,
  onClose,
  canPrevious,
  canNext,
  nextLoading = false,
}: {
  image: ImagePreviewItem | null;
  index?: number;
  total?: number;
  loading?: boolean;
  error?: string;
  onPrevious?: () => void;
  onNext?: () => void;
  onClose?: () => void;
  canPrevious?: boolean;
  canNext?: boolean;
  nextLoading?: boolean;
}) {
  const hasPrevious = canPrevious ?? index > 0;
  const hasNext = canNext ?? index < total - 1;
  const iconButtonClass = "!border-white/30 !bg-black/55 !text-white hover:!bg-black/75";
  const content = <section aria-label="图片预览" className="pointer-events-auto fixed inset-0 z-[110] grid place-items-center bg-black/80 p-4 backdrop-blur-sm">
    {onClose ? <Button type="button" size="sm" variant="ghost" aria-label="关闭图片预览" title="关闭图片预览" onClick={onClose} className={`fixed right-4 top-4 size-9 rounded-full p-0 shadow-lg ${iconButtonClass}`}>
      <X aria-hidden="true" className="size-4" /></Button> : null}
    <div className="relative grid min-h-64 w-fit max-w-full place-items-center">
      {loading ? <Spinner label="加载图片" /> : error ? <p role="alert" className="p-4 text-center text-sm text-white">{error}</p> : image ? <img src={image.url} alt={image.label} width={1920} height={1080} className="max-h-[72vh] max-w-full object-contain" /> : null}
      {total > 1 || onPrevious ? <Button type="button" size="sm" variant="ghost" aria-label="上一张" title="上一张" disabled={!hasPrevious || !onPrevious} onClick={onPrevious} className={cn(`absolute left-3 top-1/2 size-9 -translate-y-1/2 rounded-full p-0 shadow-lg ${iconButtonClass}`, !hasPrevious && "opacity-45")}>
        <ChevronLeft aria-hidden="true" className="size-5" /></Button> : null}
      {total > 1 || onNext ? <Button type="button" size="sm" variant="ghost" aria-label="下一张" title={nextLoading ? "加载中" : "下一张"} disabled={!hasNext || !onNext || nextLoading} onClick={onNext} className={cn(`absolute right-3 top-1/2 size-9 -translate-y-1/2 rounded-full p-0 shadow-lg ${iconButtonClass}`, (!hasNext || nextLoading) && "opacity-45")}>
        <ChevronRight aria-hidden="true" className={cn("size-5", nextLoading && "animate-pulse")} /></Button> : null}
    </div>
    {total > 1 ? <div className="fixed bottom-4 left-1/2 flex -translate-x-1/2 items-center gap-2 text-xs text-app-subtle">
      <span className="rounded-md bg-black/65 px-2 py-1 text-white">{index + 1} / {total}</span>
    </div> : null}
  </section>;
  return typeof document === "undefined" ? null : createPortal(content, document.body);
}
