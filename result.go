package gollector

type ResultState uint8

const (
	StateOk      ResultState = 0
	StateWarn                = 1
	StateCrit                = 2
	StateUnknown             = 3
)

func (s ResultState) String() string {
	switch s {
	case StateOk:
		return "OK"
	case StateWarn:
		return "WARN"
	case StateCrit:
		return "CRIT"
	default:
		return "UNKNOWN"
	}
}

type Result struct {
	State      ResultState
	ReasonCode string
	Metrics    []ResultMetric
}

func (r Result) justifiesNewIncidentForCheck(check Check) bool {
	// if incident suppression is on, never allow new incident
	if check.SuppressIncidents {
		return false
	}

	lastResult := check.LastResult
	lastIncident := check.Incident

	// if current result is OK, no incident
	if r.State == StateOk {
		return false
	}

	// current result NOT OK and last incident exists
	if lastIncident != nil {
		// last incident to-state different from this result state
		return lastIncident.ToState != r.State
	}

	// current result NOT OK and NO last incident exists and last result exists
	if lastResult != nil {
		// last result state different from new state
		return lastResult.State != r.State
	}

	// not ok, no last incident, no last result
	return true
}

func MakeUnknownResult(reasonCode string) *Result {
	return &Result{
		State:      StateUnknown,
		ReasonCode: reasonCode,
		Metrics:    nil,
	}
}

type ResultMetric struct {
	Label string
	Value string
}
