import React from 'react';
import { useBuilderStore } from '../store';
import type { SchemaField } from '../api';

// Sub-components — each in its own file under ./inputs/
import {
  TagMultiSelect,
  StageDropdown,
  UserDropdown,
  BooleanToggle,
  SelectDropdown,
  NumberInput,
  DateInput,
  StringInput,
  MultiValueChipInput,
  MultiSelectDropdown,
} from './inputs';

// ============================================================
// SmartValueInput — renders the right picker based on field metadata
// ============================================================

export interface SmartValueInputProps {
  /** The resolved schema field (caller passes this directly — no internal lookup) */
  field: SchemaField;
  /** The currently selected operator (e.g. 'eq', 'contains', 'gt') */
  operator: string;
  /** Current value of the condition */
  value: unknown;
  /** Callback when value changes */
  onChange: (v: unknown) => void;
}

/**
 * Schema-aware value input for workflow conditions.
 *
 * Reads picker_type + options from the field to determine which sub-component
 * to render:
 *   picker_type=tag   → TagMultiSelect (colored pills, real org tags)
 *   picker_type=stage → StageDropdown  (colored dots, real pipeline stages)
 *   picker_type=user  → UserDropdown   (avatar initials, real org members)
 *   type=boolean      → BooleanToggle  (Yes/No pill toggle)
 *   type=select       → SelectDropdown (options from schema field)
 *   type=number       → NumberInput
 *   type=date         → DateInput (UTC ISO 8601)
 *   default           → StringInput (plain text fallback)
 */
export const SmartValueInput: React.FC<SmartValueInputProps> = ({
  field,
  operator,
  value,
  onChange,
}) => {
  // Tags, stages, users live at schema root — still need store for those
  const schema = useBuilderStore((s) => s.schema);

  const fieldType = field.type;
  const pickerType = field.picker_type;
  const options = field.options;

  // 1. Picker-type based rendering (highest priority)
  if (pickerType === 'tag') {
    return (
      <TagMultiSelect
        tags={schema?.tags || []}
        value={value}
        onChange={onChange}
      />
    );
  }

  if (pickerType === 'stage') {
    return (
      <StageDropdown
        stages={schema?.stages || []}
        value={value}
        onChange={onChange}
      />
    );
  }

  if (pickerType === 'user') {
    return (
      <UserDropdown
        users={schema?.users || []}
        value={value}
        onChange={onChange}
      />
    );
  }

  // 2. Operator-aware: in/not_in → force multi-value input
  const isMultiOp = operator === 'in' || operator === 'not_in';
  if (isMultiOp) {
    // For select fields with predefined options, use a multi-select dropdown
    if (fieldType === 'select' && options && options.length > 0) {
      return <MultiSelectDropdown options={options} value={value} onChange={onChange} />;
    }
    // For all other types, use a chip input (type & press Enter)
    return (
      <MultiValueChipInput
        value={value}
        onChange={onChange}
        placeholder={fieldType === 'number' ? 'Type number + Enter' : 'Type value + Enter'}
      />
    );
  }

  // 3. Field-type based rendering
  if (fieldType === 'boolean') {
    return <BooleanToggle value={value} onChange={onChange} />;
  }

  if (fieldType === 'select' && options && options.length > 0) {
    return <SelectDropdown options={options} value={value} onChange={onChange} />;
  }

  if (fieldType === 'number') {
    return (
      <NumberInput
        value={value}
        onChange={onChange}
        min={field.min}
        max={field.max}
      />
    );
  }

  if (fieldType === 'date') {
    return <DateInput value={value} onChange={onChange} />;
  }

  // 4. Default: plain text fallback
  return <StringInput value={value} onChange={onChange} operator={operator} fieldType={fieldType} />;
};

// Re-export sub-components for use in ActionConfigPanel (P10, P16, etc.)
export {
  TagMultiSelect,
  StageDropdown,
  UserDropdown,
  BooleanToggle,
  SelectDropdown,
  NumberInput,
  DateInput,
  StringInput,
  MultiValueChipInput,
  MultiSelectDropdown,
} from './inputs';
