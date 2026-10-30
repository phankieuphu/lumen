// Small presentational pieces shared across screens.
import { type ReactNode, useEffect, useRef } from "react";

import type { Env, Kind, Status } from "../api";

export function Badge({ tone, children }: { tone: string; children: ReactNode }) {
  return <span className={`badge badge-${tone}`}>{children}</span>;
}

export const EnvBadge = ({ env }: { env: Env | string }) => <Badge tone={env}>{env}</Badge>;
export const StatusBadge = ({ status }: { status: Status }) => <Badge tone={status}>{status}</Badge>;
export const KindBadge = ({ kind }: { kind: Kind | string }) => <Badge tone={`kind-${kind}`}>{kind}</Badge>;

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
