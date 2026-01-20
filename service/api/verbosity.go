package api

// VerbosityLevel defines trace output verbosity (0-4)
type VerbosityLevel int

const (
	VerbosityNone     VerbosityLevel = 0 // No parameter output
	VerbosityTypes    VerbosityLevel = 1 // Type names only
	VerbosityInline   VerbosityLevel = 2 // Inline values, limited depth
	VerbosityExpanded VerbosityLevel = 3 // Multi-line, more depth
	VerbosityFull     VerbosityLevel = 4 // Maximum detail
)

// GetLoadConfigForVerbosity returns the LoadConfig for a given verbosity level
func GetLoadConfigForVerbosity(verbosity int) LoadConfig {
	switch VerbosityLevel(verbosity) {
	case VerbosityNone:
		return LoadConfig{
			FollowPointers:     false,
			MaxVariableRecurse: 0,
			MaxStringLen:       0,
			MaxArrayValues:     0,
			MaxStructFields:    0,
		}

	case VerbosityTypes:
		return LoadConfig{
			FollowPointers:     false,
			MaxVariableRecurse: 0,
			MaxStringLen:       32,
			MaxArrayValues:     0,
			MaxStructFields:    0, // Load structure, but don't expand fields
		}

	case VerbosityInline:
		return LoadConfig{
			FollowPointers:     false,
			MaxVariableRecurse: 1,
			MaxStringLen:       64,
			MaxArrayValues:     5,
			MaxStructFields:    3,
		}

	case VerbosityExpanded:
		return LoadConfig{
			FollowPointers:     false,
			MaxVariableRecurse: 2,
			MaxStringLen:       128,
			MaxArrayValues:     10,
			MaxStructFields:    10,
		}

	case VerbosityFull:
		return LoadConfig{
			FollowPointers:     true,
			MaxVariableRecurse: 3,
			MaxStringLen:       256,
			MaxArrayValues:     100,
			MaxStructFields:    -1, // All fields
		}

	default:
		return LoadConfig{} // Minimal config for invalid values
	}
}

// verbosityToFlags converts verbosity level to PrettyFlags for formatting
func verbosityToFlags(verbosity int) PrettyFlags {
	switch VerbosityLevel(verbosity) {
	case VerbosityNone:
		return 0 // Should not be called for level 0

	case VerbosityTypes:
		// Level 1: Just type names
		return prettyTop | prettyIncludeType | PrettyShortenType

	case VerbosityInline:
		// Level 2: Inline format with short types
		return prettyTop | prettyIncludeType | PrettyShortenType

	case VerbosityExpanded, VerbosityFull:
		// Levels 3-4: Multi-line with short types
		return prettyTop | prettyIncludeType | PrettyShortenType | PrettyNewlines

	default:
		return prettyTop | prettyIncludeType
	}
}
