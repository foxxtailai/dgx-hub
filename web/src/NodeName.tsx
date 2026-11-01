import type { Node } from './types';

export const nodeColors = ['green', 'blue', 'orange', 'red', 'yellow'] as const;

export function nodeColorClass(node?: Pick<Node, 'color'>) {
  return `node-color-${node?.color || 'green'}`;
}

export function NodeName({ node, fallback = '' }: { node?: Pick<Node, 'name' | 'color'>; fallback?: string }) {
  return <span className={node ? `node-name ${nodeColorClass(node)}` : undefined}>{fallback || node?.name}</span>;
}
