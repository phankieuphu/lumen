// Small presentational pieces shared across screens.
import { type ReactNode, useEffect, useId, useRef } from "react";

import type { Env, Kind, Status } from "../api";

export function Badge({ tone, children }: { tone: string; children: ReactNode }) {
  return <span className={`badge tone-${tone}`}>{children}</span>;
}

export const EnvBadge = ({ env }: { env: Env | string }) => <Badge tone={env}>{env}</Badge>;
export const KindBadge = ({ kind }: { kind: Kind | string }) => <Badge tone={`kind-${kind}`}>{kind}</Badge>;
export const EnvDot = ({ env }: { env: Env | string }) => <span className={`dot tone-${env}`} aria-hidden="true" />;

/** StatusText is a request outcome as colored text. */
export const StatusText = ({ status }: { status: Status }) => <span className={`status tone-${status}`}>{status}</span>;

export function Field({ label, children, wide }: { label: string; children: ReactNode; wide?: boolean }) {
  return (
    <label className={wide ? "field wide" : "field"}>
      <span>{label}</span>
      {children}
    </label>
  );
}

export const ErrorText = ({ error }: { error: string | undefined }) =>
  error ? (
    <p className="error" role="alert">
      {error}
    </p>
  ) : null;

interface SegmentedProps<T extends string> {
  label: string;
  options: readonly { value: T; label: ReactNode }[];
  value: T;
  onChange: (value: T) => void;
}

/** Segmented is a single-choice button group. */
export function Segmented<T extends string>({ label, options, value, onChange }: SegmentedProps<T>) {
  return (
    <div className="segmented" role="group" aria-label={label}>
      {options.map((o) => (
        <button key={o.value} type="button" aria-pressed={o.value === value} onClick={() => onChange(o.value)}>
          {o.label}
        </button>
      ))}
    </div>
  );
}

interface SwitchRowProps {
  label: string;
  help: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}

/** SwitchRow is a labelled on/off setting. */
export function SwitchRow({ label, help, checked, onChange }: SwitchRowProps) {
  const id = useId();
  return (
    <div className="switch-row">
      <div>
        <div id={id} className="switch-label">
          {label}
        </div>
        <div className="muted small">{help}</div>
      </div>
      <button type="button" role="switch" className="switch" aria-checked={checked} aria-labelledby={id} onClick={() => onChange(!checked)}>
        <span />
      </button>
    </div>
  );
}

interface ConfirmProps {
  title: string;
  children: ReactNode;
  confirmLabel: string;
  danger?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

/** Confirm is a modal dialog; Escape cancels. */
export function Confirm({ title, children, confirmLabel, danger, onConfirm, onCancel }: ConfirmProps) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current;
    d?.showModal();
    return () => d?.close();
  }, []);
  return (
    <dialog ref={ref} className="dialog" onCancel={onCancel}>
      <h2>{title}</h2>
      <div className="dialog-body">{children}</div>
      <div className="actions">
        <span className="spacer" />
        <button type="button" onClick={onCancel}>
          Cancel
        </button>
        <button type="button" className={danger ? "danger" : "primary"} onClick={onConfirm} autoFocus>
          {confirmLabel}
        </button>
      </div>
    </dialog>
  );
}
