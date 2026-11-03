// Small presentational pieces shared across screens.
import { type ReactNode, useEffect, useRef } from "react";

import type { Connection, Env, Kind, Status } from "../api";
import { Icon } from "./icons";

export function Badge({ tone, children }: { tone: string; children: ReactNode }) {
  return <span className={`badge badge-${tone}`}>{children}</span>;
}

export const EnvBadge = ({ env }: { env: Env | string }) => <Badge tone={env}>{env}</Badge>;
export const StatusBadge = ({ status }: { status: Status }) => <Badge tone={status}>{status}</Badge>;
export const KindBadge = ({ kind }: { kind: Kind | string }) => <Badge tone={`kind-${kind}`}>{kind}</Badge>;

/** EnvDot is the coloured environment marker; the env name is next to it or in its title. */
export const EnvDot = ({ env }: { env: Env | string }) => <span className={`dot dot-${env}`} title={env} aria-hidden="true" />;

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

/** Banner is a one-line notice across a pane, e.g. a query error. */
export function Banner({ tone, children }: { tone: "danger" | "warn" | "ok"; children: ReactNode }) {
  return (
    <div className={`banner banner-${tone}`} role={tone === "ok" ? "status" : "alert"}>
      <Icon name={tone === "ok" ? "check" : "alert"} size={15} strokeWidth={2} />
      <span>{children}</span>
    </div>
  );
}

interface SwitchProps {
  label: string;
  help?: ReactNode;
  checked: boolean;
  disabled?: boolean;
  onChange: (checked: boolean) => void;
}

/** SwitchRow is a labelled on/off setting. */
export function SwitchRow({ label, help, checked, disabled, onChange }: SwitchProps) {
  return (
    <div className="switch-row">
      <div className="switch-text">
        <div className="switch-label">{label}</div>
        {help && <div className="muted small-text">{help}</div>}
      </div>
      <button
        type="button"
        role="switch"
        className="switch"
        aria-checked={checked}
        aria-label={label}
        disabled={disabled}
        onClick={() => onChange(!checked)}
      >
        <span className="switch-knob" />
      </button>
    </div>
  );
}

interface ConnSelectProps {
  connections: readonly Connection[];
  value: string;
  onChange: (id: string) => void;
}

/** ConnSelect picks the connection a screen works on; the dot shows its env. */
export function ConnSelect({ connections, value, onChange }: ConnSelectProps) {
  const env = connections.find((c) => c.id === value)?.env ?? "";
  return (
    <span className="pill-select-wrap">
      <EnvDot env={env} />
      <select className="pill-select" aria-label="Connection" value={value} onChange={(e) => onChange(e.target.value)}>
        {connections.map((c) => (
          <option key={c.id} value={c.id}>
            {c.name}
          </option>
        ))}
      </select>
    </span>
  );
}

interface SegmentedProps<T extends string> {
  label: string;
  value: T;
  options: readonly { value: T; label: string }[];
  onChange: (value: T) => void;
}

/** Segmented is a single-choice button group. */
export function Segmented<T extends string>({ label, value, options, onChange }: SegmentedProps<T>) {
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
        <button type="button" className={danger ? "danger solid" : "primary"} onClick={onConfirm} autoFocus>
          {confirmLabel}
        </button>
      </div>
    </dialog>
  );
}
