import React from 'react';
import { useDroppable } from '@dnd-kit/core';

interface AddNodeButtonProps {
  index: number;
}

export const AddNodeButton: React.FC<AddNodeButtonProps> = ({ index }) => {
  const { isOver, setNodeRef } = useDroppable({
    id: `dropzone-${index}`,
    data: { targetIndex: index },
  });

  return (
    <div className="flex flex-col items-center py-1">
      <div className="w-px h-6 bg-gray-700" />
      <div
        ref={setNodeRef}
        className={`
          flex items-center justify-center w-8 h-8 rounded-full border-2 border-dashed
          transition-all duration-200
          ${isOver
            ? 'border-emerald-400 bg-emerald-400/20 scale-125'
            : 'border-gray-600 hover:border-gray-500 hover:bg-gray-800'}
        `}
      >
        <span className="text-gray-500 text-sm">+</span>
      </div>
      <div className="w-px h-6 bg-gray-700" />
    </div>
  );
};
