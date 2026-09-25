import { forwardRef, useImperativeHandle, useMemo, useRef } from "react";
import { highlight } from "./drill";

export interface CodeEditorHandle {
  focusAt: (offset: number) => void;
}

interface CodeEditorProps {
  value: string;
  errorLine: number | null;
  onChange: (value: string) => void;
  onSubmit: () => void;
}

export const CodeEditor = forwardRef<CodeEditorHandle, CodeEditorProps>(function CodeEditor(
  { value, errorLine, onChange, onSubmit },
  ref,
) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const layerRef = useRef<HTMLPreElement>(null);
  const gutterRef = useRef<HTMLDivElement>(null);
  const tokens = useMemo(() => highlight(value), [value]);
  const lineCount = value.split("\n").length;

  useImperativeHandle(ref, () => ({
    focusAt(offset: number) {
      const textarea = textareaRef.current;
      if (!textarea) return;
      textarea.focus();
      textarea.setSelectionRange(offset, offset);
      const line = value.slice(0, offset).split("\n").length;
      textarea.scrollTop = Math.max(0, (line - 4) * 20);
    },
  }));

  function syncScroll() {
    const textarea = textareaRef.current;
    if (!textarea) return;
    if (layerRef.current) {
      layerRef.current.scrollTop = textarea.scrollTop;
      layerRef.current.scrollLeft = textarea.scrollLeft;
    }
    if (gutterRef.current) gutterRef.current.scrollTop = textarea.scrollTop;
  }

  return (
    <div className="code-editor">
      <div className="code-gutter" aria-hidden="true" ref={gutterRef}>
        {Array.from({ length: lineCount }, (_, index) => (
          <span key={index} className={errorLine === index + 1 ? "gutter-error" : undefined}>{index + 1}</span>
        ))}
      </div>
      <div className="code-surface">
        <pre className="code-layer" aria-hidden="true" ref={layerRef}>
          {tokens.map((token, index) => (
            <span key={index} className={`tok-${token.kind}`}>{token.text}</span>
          ))}
          {"\n"}
        </pre>
        <textarea
          ref={textareaRef}
          className="code-input"
          aria-label="Drill definition JSON"
          autoCapitalize="off"
          autoComplete="off"
          autoCorrect="off"
          spellCheck={false}
          value={value}
          onScroll={syncScroll}
          onChange={(event) => onChange(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
              event.preventDefault();
              onSubmit();
            }
          }}
        />
      </div>
    </div>
  );
});
