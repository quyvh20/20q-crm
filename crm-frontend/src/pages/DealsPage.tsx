import { ObjectListView } from '../features/objects';

// Deals render through the shared renderer (P7). The kanban board (drag to change
// stage, which routes through ChangeStage + fires deal_stage_changed on the backend)
// lives in ObjectListView/ObjectKanban via the Table/Board toggle. The
// objects.unified_read flag was removed once parity was reached.
export default function DealsPage() {
  return <ObjectListView slug="deal" />;
}
