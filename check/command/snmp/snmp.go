package snmp

import (
	"fmt"
	"github.com/seankndy/gopoller/check"
	"github.com/seankndy/gopoller/snmp"
	"math/big"
	"strings"
)

type OidMonitor struct {
	Oid               string
	Name              string
	PostProcessValue  float64
	WarnMinThreshold  float64
	CritMinThreshold  float64
	WarnMaxThreshold  float64
	CritMaxThreshold  float64
	WarnMinReasonCode string
	CritMinReasonCode string
	WarnMaxReasonCode string
	CritMaxReasonCode string
}

func NewOidMonitor(oid, name string) *OidMonitor {
	return &OidMonitor{
		Oid:              oid,
		Name:             name,
		PostProcessValue: 1.0,
	}
}

func (o *OidMonitor) determineResultStateAndReasonFromResultValue(value *big.Float) (check.ResultState, string) {
	if o.CritMinReasonCode != "" && value.Cmp(big.NewFloat(o.CritMinThreshold)) < 0 {
		return check.StateCrit, o.CritMinReasonCode
	} else if o.WarnMinReasonCode != "" && value.Cmp(big.NewFloat(o.WarnMinThreshold)) < 0 {
		return check.StateWarn, o.WarnMinReasonCode
	} else if o.CritMaxReasonCode != "" && value.Cmp(big.NewFloat(o.CritMaxThreshold)) > 0 {
		return check.StateCrit, o.CritMaxReasonCode
	} else if o.WarnMaxReasonCode != "" && value.Cmp(big.NewFloat(o.WarnMaxThreshold)) > 0 {
		return check.StateWarn, o.WarnMaxReasonCode
	}

	return check.StateOk, ""
}

type Command struct {
	getter snmp.Getter

	Host        snmp.Host
	OidMonitors []OidMonitor
}

func (c *Command) SetGetter(getter snmp.Getter) {
	c.getter = getter
}

func NewCommand(addr, community string, monitors []OidMonitor) *Command {
	return &Command{
		Host:        *snmp.NewHost(addr, community),
		OidMonitors: monitors,
	}
}

func (c *Command) Run(chk *check.Check) (*check.Result, error) {
	var getter snmp.Getter
	if c.getter == nil {
		getter = snmp.DefaultGetter
	} else {
		getter = c.getter
	}

	// create a map of oid->oidMonitors for fast OidMonitor lookup when processing the result values below
	oidMonitorsByOid := make(map[string]*OidMonitor, len(c.OidMonitors))
	// build raw slice of oids from c.OidMonitors to pass to getSnmpObjects()
	rawOids := make([]string, len(c.OidMonitors))
	for k, _ := range c.OidMonitors {
		rawOids[k] = c.OidMonitors[k].Oid
		oidMonitorsByOid[c.OidMonitors[k].Oid] = &c.OidMonitors[k]
	}

	chk.Debugf("oid(s) to fetch: %s", rawOids)

	objects, err := getter.Get(&c.Host, rawOids)
	if err != nil {
		if strings.Contains(err.Error(), "request timeout") {
			return check.MakeUnknownResult("CONNECTION_ERROR"), nil
		}
		return check.MakeUnknownResult("CMD_FAILURE"), err
	}

	var resultMetrics []check.ResultMetric
	resultState := check.StateUnknown
	var resultReason string

	for _, object := range objects {
		chk.Debugf("got oid=%s value=%v", object.Oid, object.Value)

		oidMonitor := oidMonitorsByOid[object.Oid]
		if oidMonitor == nil {
			if object.Oid[:1] == "." {
				oidMonitor = oidMonitorsByOid[object.Oid[1:]]
			}
		}
		if oidMonitor == nil {
			return check.MakeUnknownResult("CMD_FAILURE"),
				fmt.Errorf("snmp.Command.Run(): oid %s could not be found in monitors", object.Oid)
		}

		var resultMetricValue string
		var resultMetricType check.ResultMetricType

		// for counter types, we compare the difference between the last result and this current result to the
		// monitor's thresholds, and also we do not apply PostProcessValue to the result
		// for non-counter types, we compare the raw value to the monitor thresholds, and we do apply PostProcessValue
		// to the value

		if object.Type == snmp.Counter64 || object.Type == snmp.Counter32 {
			value := snmp.ToBigInt(object.Value)

			resultMetricType = check.ResultMetricCounter
			resultMetricValue = value.Text(10)

			// if state is still Unknown, check if this snmp object exceeds any thresholds
			if resultState == check.StateUnknown {
				// get last metric to calculate difference
				lastMetric := getChecksLastResultMetricByLabel(chk, oidMonitor.Name)
				var lastValue *big.Int
				if lastMetric != nil {
					var ok bool
					lastValue, ok = new(big.Int).SetString(lastMetric.Value, 10)
					if !ok {
						lastValue = big.NewInt(0)
					}
				} else {
					lastValue = big.NewInt(0)
				}

				// calculate the difference between previous and current result value, accounting for rollover
				var diff *big.Int
				if object.Type == snmp.Counter64 {
					diff = snmp.CalculateCounterDiff(lastValue, value, 64)
				} else {
					diff = snmp.CalculateCounterDiff(lastValue, value, 32)
				}

				resultState, resultReason = oidMonitor.determineResultStateAndReasonFromResultValue(convertBigIntToBigFloat(diff))
			}
		} else {
			var value *big.Float
			if strValue, ok := object.Value.(string); ok {
				chk.Debugf("gauge oid %s is a string value (%s)", object.Oid, strValue)

				strValue = strings.TrimSpace(strValue)
				value = new(big.Float).SetPrec(64)
				if _, ok = value.SetString(strValue); !ok {
					chk.Debugf("failed to parse string (%s) into big float", strValue)
					value = big.NewFloat(0)
				} else {
					chk.Debugf("string (%s) parsed to big float (%s)", strValue, value.String())
				}
			} else {
				chk.Debugf("gauge value %v is not a string, assuming it's an integer", object.Value)

				value = convertBigIntToBigFloat(snmp.ToBigInt(object.Value))
			}

			resultMetricType = check.ResultMetricGauge

			// if state is still Unknown, check if this snmp object exceeds any thresholds
			if resultState == check.StateUnknown {
				resultState, resultReason = oidMonitor.determineResultStateAndReasonFromResultValue(value)
			}

			// multiply object value by the post-process value, but only for non-counter types
			resultMetricValue = value.Mul(value, big.NewFloat(oidMonitor.PostProcessValue)).Text('f', -1)
		}

		resultMetrics = append(resultMetrics, check.ResultMetric{
			Label: oidMonitor.Name,
			Value: resultMetricValue,
			Type:  resultMetricType,
		})

	}

	return check.NewResult(resultState, resultReason, resultMetrics), nil
}

func getChecksLastResultMetricByLabel(chk *check.Check, label string) *check.ResultMetric {
	if chk.LastResult != nil {
		for k, _ := range chk.LastResult.Metrics {
			if chk.LastResult.Metrics[k].Label == label {
				return &chk.LastResult.Metrics[k]
			}
		}
	}

	return nil
}

func convertBigIntToBigFloat(bigInt *big.Int) *big.Float {
	return new(big.Float).SetPrec(uint(bigInt.BitLen())).SetInt(bigInt)
}
