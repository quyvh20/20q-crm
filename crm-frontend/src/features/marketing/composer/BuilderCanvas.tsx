import React from 'react';
import { useDroppable } from '@dnd-kit/core';
import { SortableContext, verticalListSortingStrategy } from '@dnd-kit/sortable';
import { ShieldCheck } from 'lucide-react';
import { useBuilderStore } from './builderStore';
import type { BlockAddress } from './blockUtils';
import { CanvasBlock, DropIndicator } from './CanvasBlock';
import type { VariableGroup } from './mergeScope';

interface Props {
  variableGroups: VariableGroup[];
  dropHint: BlockAddress | null;
  readOnly?: boolean;
}

/** BuilderCanvas is the WYSIWYG email surface: a fixed 600px white "email" frame
 *  (the width MJML compiles to) on a muted backdrop. Blocks render with the
 *  compiled email's typography and spacing; the compliance footer is shown but
 *  locked (it is compiler-owned and always appended server-side). */
export const BuilderCanvas: React.FC<Props> = ({ variableGroups, dropHint, readOnly }) => {
  const blocks = useBuilderStore((s) => s.blocks);
  const select = useBuilderStore((s) => s.select);

  const { setNodeRef, isOver } = useDroppable({
    id: 'root',
    data: { kind: 'container', parentId: null, colIndex: 0 },
    disabled: readOnly,
  });

  const rootHint = dropHint && dropHint.parentId === null ? dropHint : null;

  return (
    <div
      // overflow-x-auto + fixed 600px frame: at narrow app widths the email
      // pans instead of shrinking — a squeezed frame would re-wrap every line
      // and quietly stop matching what MJML compiles.
      className="min-h-0 flex-1 overflow-y-auto overflow-x-auto bg-muted/50 px-6 py-8"
      onClick={() => select(null)}
      data-testid="builder-canvas"
    >
      <div ref={setNodeRef} className="email-canvas mx-auto w-[600px] shrink-0 rounded-lg bg-white shadow-md ring-1 ring-black/5">
        <SortableContext items={blocks.map((b) => b.id)} strategy={verticalListSortingStrategy}>
          {blocks.length === 0 ? (
            <div
              className={`m-6 flex h-40 items-center justify-center rounded-lg border-2 border-dashed text-sm ${
                isOver ? 'border-primary text-primary' : 'email-hint'
              }`}
            >
              Drag a block here to start your email
            </div>
          ) : (
            blocks.map((b, i) => (
              <React.Fragment key={b.id}>
                {rootHint && rootHint.index === i && <DropIndicator />}
                <CanvasBlock
                  block={b}
                  parentId={null}
                  colIndex={0}
                  index={i}
                  listLength={blocks.length}
                  variableGroups={variableGroups}
                  dropHint={dropHint}
                  readOnly={readOnly}
                />
              </React.Fragment>
            ))
          )}
          {rootHint && rootHint.index === blocks.length && blocks.length > 0 && <DropIndicator />}
        </SortableContext>

        {/* Compliance footer — compiler-owned, always appended, never editable */}
        <div className="group/footer relative" onClick={(e) => e.stopPropagation()}>
          <div style={{ padding: '24px 0 0' }}>
            <div style={{ padding: '0 25px 12px' }}>
              <div style={{ borderTop: '1px solid #e5e7eb' }} />
            </div>
            <div style={{ padding: '10px 25px 20px', textAlign: 'center', fontSize: '12px', color: '#6b7280', lineHeight: 1.5 }}>
              Your workspace name
              <br />
              Your postal address
              <br />
              <span style={{ color: '#6b7280', textDecoration: 'underline' }}>Unsubscribe</span>
            </div>
          </div>
          <div className="pointer-events-none absolute inset-0 flex items-center justify-center opacity-0 transition-opacity group-hover/footer:opacity-100">
            <span className="flex items-center gap-1.5 rounded-full border border-border bg-card px-2.5 py-1 text-[11px] font-medium text-muted-foreground shadow-sm">
              <ShieldCheck className="h-3.5 w-3.5" />
              Compliance footer — added automatically, can’t be removed
            </span>
          </div>
        </div>
      </div>
    </div>
  );
};

export default BuilderCanvas;
