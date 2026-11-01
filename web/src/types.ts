export interface Node { id: string; name: string; ssh_target: string; data_path: string }
export interface Service { id: string; name: string; node_id: string; remote_host: string; remote_port: number; scheme: string; path: string }
export interface Memory { used: number; total: number; percent: number }
export interface Disk extends Memory { path: string; mount: string }
export interface Metrics {
  node_id: string; status: string; last_success?: string; stale: boolean; cpu: number | null;
  gpu: number | null; memory: Memory | null; disks: Disk[] | null; errors: Record<string, string> | null;
}
export interface SetupStep { title: string; description: string; command?: string }
export interface SSHCheck { ok: boolean; code: string; message: string; command: string; steps: SetupStep[] }
export interface Session { csrf_token: string; launch_token: string; version: string }
export interface TerminalResult { ok: boolean; command: string; error?: string }
export type NodeInput = Omit<Node, 'id'>;
export type ServiceInput = Omit<Service, 'id'>;

export interface ActionStep { command: string; name: string; timeout_seconds: number; on_error: 'stop' | 'continue' }
export interface Action { id: string; name: string; description: string; steps: ActionStep[]; node_ids: string[]; confirm_before_run: boolean }
export type ActionInput = Omit<Action, 'id'>;
export type ActionStatus = 'pending' | 'running' | 'success' | 'failed' | 'skipped' | 'cancelled';
export interface ActionStepResult extends ActionStep {
  status: ActionStatus; started_at?: string; finished_at?: string; duration_ms: number; exit_code: number | null;
  stdout: string; stderr: string; error: string; stdout_truncated: boolean; stderr_truncated: boolean;
}
export interface ActionRun {
  id: string; action_id: string; action_name: string; node_id: string; node_name: string;
  status: 'running' | 'success' | 'failed' | 'cancelled'; started_at: string; finished_at?: string;
  duration_ms: number; error: string; steps: ActionStepResult[];
}
