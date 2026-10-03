import { Wrench } from 'lucide-react';
import { Button } from './ui/button';
import { Menu } from './ui/menu';
import { Tip } from './ui/tooltip';

export function ComposerTools() {
  return (
    <Menu.Root modal={false}>
      <Tip label="Tools">
        <Menu.Trigger render={<Button size="icon" variant="subtle" className="text-muted" aria-label="Tools" />}><Wrench /></Menu.Trigger>
      </Tip>
      <Menu.Content aria-label="Tools" side="top" align="start" className="p-2">
        <p className="px-2 py-1 text-ui text-muted">No tools available.</p>
      </Menu.Content>
    </Menu.Root>
  );
}
