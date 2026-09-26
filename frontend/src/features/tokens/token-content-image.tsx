import { useEffect, useRef, useState } from "react";
import ImageIcon from "lucide-react/dist/esm/icons/image.mjs";
import { Button, ImagePreview, Spinner, type ImagePreviewItem } from "../../components/ui";
import { api, errorMessage } from "../../lib/api";
import { contentPath } from "./content-types";

export async function fetchTokenImage(tokenId: string, fileId: string, signal?: AbortSignal): Promise<string> {
  const data = await api<{ image: { data_url: string } }>(`${contentPath(tokenId)}/files/${encodeURIComponent(fileId)}/image`, { signal });
  if (!/^data:image\/(png|jpeg|webp|gif|avif);base64,/.test(data.image.data_url)) throw new Error("图片格式不支持预览");
  return data.image.data_url;
}

export function TokenContentImage({ tokenId, fileId, label = "聊天图片", onPreview }: {
  tokenId: string;
  fileId: string;
  label?: string;
  onPreview: (image: { url: string; label: string }) => void;
}) {
  const container = useRef<HTMLDivElement>(null);
  const [visible, setVisible] = useState(false);
  const [url, setURL] = useState("");
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    const element = container.current;
    if (!element || typeof IntersectionObserver === "undefined") {
      setVisible(true);
      return;
    }
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) {
        setVisible(true);
        observer.disconnect();
      }
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    if (!visible) return;
    const controller = new AbortController();
    setError("");
    setURL("");
    void fetchTokenImage(tokenId, fileId, controller.signal)
      .then((dataURL) => {
        if (!controller.signal.aborted) setURL(dataURL);
      })
      .catch((failure: unknown) => { if (!controller.signal.aborted) setError(errorMessage(failure)); });
    return () => controller.abort();
  }, [tokenId, fileId, visible, retry]);
  return <div ref={container} className="grid size-40 shrink-0 place-items-center overflow-hidden rounded-lg border border-app-border bg-app-muted">
    {url ? <Button variant="ghost" className="!h-full w-full !p-0" aria-label={`放大${label}`} onClick={() => onPreview({ url, label })}>
      <img src={url} alt={label} width={160} height={160} loading="lazy" className="size-full object-cover" onError={() => { setURL(""); setError("图片无法显示，请重试"); }} />
    </Button> : error ? <div className="grid gap-2 p-3 text-center">
      <ImageIcon aria-hidden="true" className="mx-auto size-5 text-app-subtle" />
      <p className="line-clamp-3 text-xs text-app-subtle" title={error}>{error}</p>
      <Button size="sm" onClick={() => setRetry((value) => value + 1)}>重试图片</Button>
    </div> : <Spinner label="加载图片" />}
  </div>;
}

export function TokenImagePreview({ image, onClose, index = 0, total = 1, onPrevious, onNext }: {
  image: ImagePreviewItem | null;
  onClose: () => void;
  index?: number;
  total?: number;
  onPrevious?: () => void;
  onNext?: () => void;
}) {
  return <ImagePreview image={image} index={index} total={total} onPrevious={onPrevious} onNext={onNext} onClose={onClose} />;
}

export function TokenContentImagePreview({ tokenId, fileId, label, index = 0, total = 1, onPrevious, onNext, onClose }: {
  tokenId: string;
  fileId: string;
  label: string;
  index?: number;
  total?: number;
  onPrevious?: () => void;
  onNext?: () => void;
  onClose: () => void;
}) {
  const [url, setURL] = useState("");
  const [error, setError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    setURL("");
    setError("");
    void fetchTokenImage(tokenId, fileId, controller.signal)
      .then((dataURL) => {
        if (!controller.signal.aborted) setURL(dataURL);
      })
      .catch((failure: unknown) => {
        if (!controller.signal.aborted) setError(errorMessage(failure));
      });
    return () => controller.abort();
  }, [tokenId, fileId]);
  return <ImagePreview image={url ? { url, label } : null} index={index} total={total} loading={!url && !error} error={error} onPrevious={onPrevious} onNext={onNext} onClose={onClose} />;
}
