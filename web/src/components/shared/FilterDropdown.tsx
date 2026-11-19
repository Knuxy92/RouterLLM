import { ChevronDownIcon } from "../icons"

interface FilterDropdownProps {
  options: string[]
  selected: string
  open: boolean
  onToggle: () => void
  onPick: (value: string) => void
}

export default function FilterDropdown({ options, selected, open, onToggle, onPick }: FilterDropdownProps) {
  return (
    <div className={`dd ${open ? "open" : ""}`}>
      <button type="button" className="dd-btn" onClick={onToggle}>
        <span className="dd-label">{selected}</span>
        <ChevronDownIcon />
      </button>
      <div className="dd-menu">
        {options.map((option) => (
          <button
            type="button"
            key={option}
            className={`dd-item${option === selected ? " active" : ""}`}
            onClick={() => onPick(option)}
          >
            {option}
          </button>
        ))}
      </div>
    </div>
  )
}
