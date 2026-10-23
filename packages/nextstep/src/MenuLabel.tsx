import type { MenuItem } from './types';

export function MenuLabel({
  label,
  description,
}: Pick<MenuItem, 'label' | 'description'>) {
  return (
    <span className="ns-menu-label">
      {description ? (
        <>
          <span className="ns-menu-name">{label}</span>{' '}
          <span className="ns-menu-description">{description}</span>
        </>
      ) : (
        label
      )}
    </span>
  );
}
