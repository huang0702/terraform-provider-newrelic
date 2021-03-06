package newrelic

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/helper/validation"
	"github.com/newrelic/newrelic-client-go/pkg/alerts"
	"github.com/newrelic/newrelic-client-go/pkg/errors"
)

func resourceNewRelicNrqlAlertCondition() *schema.Resource {
	return &schema.Resource{
		Create: resourceNewRelicNrqlAlertConditionCreate,
		Read:   resourceNewRelicNrqlAlertConditionRead,
		Update: resourceNewRelicNrqlAlertConditionUpdate,
		Delete: resourceNewRelicNrqlAlertConditionDelete,
		Importer: &schema.ResourceImporter{
			State: resourceImportStateWithMetadata(2, "type"),
		},
		Schema: map[string]*schema.Schema{
			"policy_id": {
				Type:        schema.TypeInt,
				Required:    true,
				ForceNew:    true,
				Description: "The ID of the policy where this condition should be used.",
			},
			"name": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "The title of the condition.",
			},
			"runbook_url": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Runbook URL to display in notifications.",
			},
			"enabled": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: "Whether or not to enable the alert condition.",
			},
			// Note: The "outlier" type does NOT exist in NerdGraph yet
			"type": {
				Type:         schema.TypeString,
				Optional:     true,
				Default:      "static",
				Description:  "The type of NRQL alert condition to create. Valid values are: 'static', 'outlier', 'baseline'.",
				ValidateFunc: validation.StringInSlice([]string{"static", "outlier", "baseline"}, false),
			},
			"nrql": {
				Type:        schema.TypeList,
				Required:    true,
				MinItems:    1,
				MaxItems:    1,
				Description: "A NRQL query.",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"query": {
							Type:     schema.TypeString,
							Required: true,
						},
						"since_value": {
							Deprecated:    "use `evaluation_offset` attribute instead",
							Type:          schema.TypeString,
							Optional:      true,
							Description:   "NRQL queries are evaluated in one-minute time windows. The start time depends on the value you provide in the NRQL condition's `since_value`.",
							ConflictsWith: []string{"nrql.0.evaluation_offset"},
							ValidateFunc: func(val interface{}, key string) (warns []string, errs []error) {
								valueString := val.(string)
								v, err := strconv.Atoi(valueString)
								if err != nil {
									errs = append(errs, fmt.Errorf("error converting string to int: %#v", err))
								}
								if v < 1 || v > 20 {
									errs = append(errs, fmt.Errorf("%q must be between 0 and 20 inclusive, got: %d", key, v))
								}
								return
							},
						},
						// New attribute in NerdGraph. Equivalent to `since_value`.
						"evaluation_offset": {
							Type:          schema.TypeInt,
							Optional:      true,
							Description:   "NRQL queries are evaluated in one-minute time windows. The start time depends on the value you provide in the NRQL condition's `evaluation_offset`.",
							ConflictsWith: []string{"nrql.0.since_value"},
							ValidateFunc: func(val interface{}, key string) (warns []string, errs []error) {
								v := val.(int)
								if v < 1 || v > 20 {
									errs = append(errs, fmt.Errorf("%q must be between 0 and 20 inclusive, got: %d", key, v))
								}
								return
							},
						},
					},
				},
			},
			"term": {
				Type:        schema.TypeSet,
				Required:    true,
				MinItems:    1,
				MaxItems:    2,
				Description: "A set of terms for this condition. Max 2 terms allowed - at least one 1 critical term and 1 optional warning term.",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						// Maps to `thresholdDuration` in NerdGraph and values are in seconds, not minutes.
						// Validation is different in NerdGraph - Value must be within 120-3600 seconds (2-60 minutes) and a multiple of 60 for BASELINE conditions.
						// Convert to seconds when using NerdGraph
						"duration": {
							Deprecated:    "use `threshold_duration` attribute instead",
							Type:          schema.TypeInt,
							Optional:      true,
							Description:   "In minutes, must be in the range of 1 to 120 (inclusive).",
							ConflictsWith: []string{"term.0.threshold_duration"},
							ValidateFunc:  validation.IntBetween(1, 120),
						},
						// Value must be uppercase when using NerdGraph
						"operator": {
							Type:         schema.TypeString,
							Optional:     true,
							Default:      "equal",
							Description:  "One of (above, below, equal). Defaults to 'equal'.",
							ValidateFunc: validation.StringInSlice([]string{"above", "below", "equal"}, false),
						},
						// Value must be uppercase when using NerdGraph
						"priority": {
							Type:         schema.TypeString,
							Optional:     true,
							Default:      "critical",
							Description:  "One of (critical, warning). Defaults to 'critical'. At least one condition term must have priority set to 'critical'.",
							ValidateFunc: validation.StringInSlice([]string{"critical", "warning"}, false),
						},
						"threshold": {
							Type:         schema.TypeFloat,
							Required:     true,
							Description:  "Must be 0 or greater. For baseline conditions must be in range [1, 1000].",
							ValidateFunc: float64Gte(0.0),
						},
						// Does not exist in NerdGraph. Equivalent to `threshold_occurrences`,
						// but with different wording.
						"time_function": {
							Deprecated:    "use `threshold_occurrences` attribute instead",
							Type:          schema.TypeString,
							Optional:      true,
							Description:   "Valid values are: 'all' or 'any'",
							ConflictsWith: []string{"term.0.threshold_occurrences"},
							ValidateFunc:  validation.StringInSlice([]string{"all", "any"}, false),
						},

						// NerdGraph only. Equivalent to `time_function`,
						// but with slightly different wording.
						// i.e. `any` (old) vs `AT_LEAST_ONCE` (new)
						"threshold_occurrences": {
							Type:          schema.TypeString,
							Optional:      true,
							Description:   "The criteria for how many data points must be in violation for the specified threshold duration. Valid values are: 'ALL' or 'AT_LEAST_ONCE' (case insensitive).",
							ConflictsWith: []string{"term.0.time_function"},
							ValidateFunc:  validation.StringInSlice([]string{"ALL", "AT_LEAST_ONCE"}, true),
						},
						// NerdGraph only. Equivalent to `duration`, but in seconds
						"threshold_duration": {
							Type:          schema.TypeInt,
							Optional:      true,
							Description:   "The duration of time, in seconds, that the threshold must violate for in order to create a violation. Value must be a multiple of 60 and within 120-3600 seconds for baseline conditions and 120-7200 seconds for static conditions.",
							ConflictsWith: []string{"term.0.duration"},
							ValidateFunc: func(val interface{}, key string) (warns []string, errs []error) {
								v := val.(int)

								// Value must be a factor of 60.
								if v%60 != 0 {
									errs = append(errs, fmt.Errorf("%q must be a factor of 60, got: %d", key, v))
								}

								// This validation is a top-level validation check.
								// Baseline conditions must be within range [120, 3600].
								// Baseline condition validation lives in the "expand" functions.
								if v < 120 || v > 7200 {
									errs = append(errs, fmt.Errorf("%q must be between 120 and 7200 inclusive, got: %d", key, v))
								}

								return
							},
						},
					},
				},
			},
			// Outlier ONLY
			"expected_groups": {
				Type:        schema.TypeInt,
				Optional:    true,
				Description: "Number of expected groups when using outlier detection.",
			},
			// Outlier ONLY
			"ignore_overlap": {
				Type:        schema.TypeBool,
				Optional:    true,
				Description: "Whether to look for a convergence of groups when using outlier detection.",
			},
			"violation_time_limit_seconds": {
				Deprecated:    "use `violation_time_limit` attribute instead",
				Type:          schema.TypeInt,
				Optional:      true,
				Description:   "Sets a time limit, in seconds, that will automatically force-close a long-lasting violation after the time limit you select. Possible values are 3600, 7200, 14400, 28800, 43200, and 86400.",
				ConflictsWith: []string{"violation_time_limit"},
				ValidateFunc:  validation.IntInSlice([]int{3600, 7200, 14400, 28800, 43200, 86400}),
			},
			// Exists in NerdGraph, but with different values. Conversion
			// between new:old and old:new is handled via maps in structures file.
			// Conflicts with `baseline_direction` when using NerdGraph.
			"value_function": {
				Type:         schema.TypeString,
				Optional:     true,
				Default:      "single_value",
				Description:  "Valid values are: 'single_value' or 'sum'",
				ValidateFunc: validation.StringInSlice([]string{"single_value", "sum"}, true),
				DiffSuppressFunc: func(k, old, new string, d *schema.ResourceData) bool {
					return strings.EqualFold(old, new) // Case fold this attribute when diffing
				},
			},
			"account_id": {
				Type:        schema.TypeInt,
				Optional:    true,
				Description: "The New Relic account ID for managing your NRQL alert conditions.",
				DefaultFunc: func() (interface{}, error) {
					envAcctID := os.Getenv("NEWRELIC_ACCOUNT_ID")
					if envAcctID != "" {
						acctID, err := strconv.Atoi(envAcctID)

						return acctID, err
					}

					return nil, nil
				},
			},
			"description": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The description of the NRQL alert condition.",
			},
			"violation_time_limit": {
				Type:          schema.TypeString,
				Optional:      true,
				Description:   "Sets a time limit, in hours, that will automatically force-close a long-lasting violation after the time limit you select. Possible values are 'ONE_HOUR', 'TWO_HOURS', 'FOUR_HOURS', 'EIGHT_HOURS', 'TWELVE_HOURS', 'TWENTY_FOUR_HOURS' (case insensitive).",
				ConflictsWith: []string{"violation_time_limit_seconds"},
				ValidateFunc:  validation.StringInSlice([]string{"ONE_HOUR", "TWO_HOURS", "FOUR_HOURS", "EIGHT_HOURS", "TWELVE_HOURS", "TWENTY_FOUR_HOURS"}, true),
				DiffSuppressFunc: func(k, old, new string, d *schema.ResourceData) bool {
					return strings.EqualFold(old, new) // Case fold this attribute when diffing
				},
			},
			// Baseline ONLY
			"baseline_direction": {
				Type:          schema.TypeString,
				Optional:      true,
				Description:   "The baseline direction of a baseline NRQL alert condition. Valid values are: 'LOWER_ONLY', 'UPPER_AND_LOWER', 'UPPER_ONLY' (case insensitive).",
				ConflictsWith: []string{"value_function"},
				ValidateFunc:  validation.StringInSlice([]string{"LOWER_ONLY", "UPPER_AND_LOWER", "UPPER_ONLY"}, true),
				DiffSuppressFunc: func(k, old, new string, d *schema.ResourceData) bool {
					return strings.EqualFold(old, new) // Case fold this attribute when diffing
				},
			},
		},
	}
}

func resourceNewRelicNrqlAlertConditionCreate(d *schema.ResourceData, meta interface{}) error {
	providerConfig := meta.(*ProviderConfig)
	client := providerConfig.NewClient

	policyID := d.Get("policy_id").(int)
	conditionType := d.Get("type").(string)

	if canUseNerdGraphNrqlAlertConditions(providerConfig, conditionType) {
		accountID := selectAccountID(providerConfig, d)

		conditionInput, err := expandNrqlAlertConditionInput(d)
		if err != nil {
			return err
		}

		log.Printf("[INFO] Creating New Relic NRQL alert condition %s via NerdGraph API", conditionInput.Name)

		id := strconv.Itoa(policyID)

		var nrqlCondition *alerts.NrqlAlertCondition
		if conditionType == "baseline" {
			if nrqlCondition, err = client.Alerts.CreateNrqlConditionBaselineMutation(accountID, id, *conditionInput); err != nil {
				return err
			}
		}

		if conditionType == "static" {
			if nrqlCondition, err = client.Alerts.CreateNrqlConditionStaticMutation(accountID, id, *conditionInput); err != nil {
				return err
			}
		}

		conditionID, err := strconv.Atoi(nrqlCondition.ID)
		if err != nil {
			return err
		}

		d.SetId(serializeIDs([]int{policyID, conditionID}))

		return resourceNewRelicNrqlAlertConditionRead(d, meta)
	}

	// Fallback to REST API
	condition := expandNrqlAlertConditionStruct(d)

	log.Printf("[INFO] Creating New Relic NRQL alert condition %s via REST API", condition.Name)

	condition, err := client.Alerts.CreateNrqlCondition(policyID, *condition)
	if err != nil {
		return err
	}

	d.SetId(serializeIDs([]int{policyID, condition.ID}))

	return resourceNewRelicNrqlAlertConditionRead(d, meta)
}

func resourceNewRelicNrqlAlertConditionRead(d *schema.ResourceData, meta interface{}) error {
	providerConfig := meta.(*ProviderConfig)
	client := providerConfig.NewClient

	log.Printf("[INFO] Reading New Relic NRQL alert condition %s", d.Id())

	ids, err := parseHashedIDs(d.Id())
	if err != nil {
		return err
	}

	policyID := ids[0]
	conditionID := strconv.Itoa(ids[1])
	conditionType := d.Get("type").(string)

	// NerdGraph
	if canUseNerdGraphNrqlAlertConditions(providerConfig, conditionType) {
		accountID := selectAccountID(providerConfig, d)

		var nrqlCondition *alerts.NrqlAlertCondition
		nrqlCondition, err = client.Alerts.GetNrqlConditionQuery(accountID, conditionID)
		if err != nil {
			if _, ok := err.(*errors.NotFound); ok {
				d.SetId("")
				return nil
			}
			return err
		}

		return flattenNrqlAlertCondition(accountID, nrqlCondition, d)
	}

	// Fallback to REST API
	_, err = client.Alerts.GetPolicy(policyID)
	if err != nil {
		if _, ok := err.(*errors.NotFound); ok {
			d.SetId("")
			return nil
		}
		return err
	}

	id := ids[1]

	condition, err := client.Alerts.GetNrqlCondition(policyID, id)
	if err != nil {
		if _, ok := err.(*errors.NotFound); ok {
			d.SetId("")
			return nil
		}

		return err
	}

	d.Set("policy_id", policyID)

	return flattenNrqlConditionStruct(condition, d)
}

func resourceNewRelicNrqlAlertConditionUpdate(d *schema.ResourceData, meta interface{}) error {
	providerConfig := meta.(*ProviderConfig)
	client := providerConfig.NewClient

	ids, err := parseHashedIDs(d.Id())
	if err != nil {
		return err
	}

	conditionID := ids[1]
	conditionType := d.Get("type").(string)

	if canUseNerdGraphNrqlAlertConditions(providerConfig, conditionType) {
		accountID := selectAccountID(providerConfig, d)

		var conditionInput *alerts.NrqlConditionInput
		conditionInput, err = expandNrqlAlertConditionInput(d)
		if err != nil {
			return err
		}

		id := strconv.Itoa(conditionID)

		if conditionType == "baseline" {
			_, err = client.Alerts.UpdateNrqlConditionBaselineMutation(accountID, id, *conditionInput)
			if err != nil {
				return err
			}
		}

		if conditionType == "static" {
			_, err = client.Alerts.UpdateNrqlConditionStaticMutation(accountID, id, *conditionInput)
			if err != nil {
				return err
			}
		}

		return resourceNewRelicNrqlAlertConditionRead(d, meta)
	}

	// Fallback to REST API
	condition := expandNrqlAlertConditionStruct(d)
	condition.ID = conditionID

	log.Printf("[INFO] Updating New Relic NRQL alert condition %d", condition.ID)

	_, err = client.Alerts.UpdateNrqlCondition(*condition)
	if err != nil {
		return err
	}

	return resourceNewRelicNrqlAlertConditionRead(d, meta)
}

func resourceNewRelicNrqlAlertConditionDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*ProviderConfig).NewClient

	ids, err := parseHashedIDs(d.Id())
	if err != nil {
		return err
	}

	conditionID := ids[1]

	log.Printf("[INFO] Deleting New Relic NRQL alert condition %d", conditionID)

	_, err = client.Alerts.DeleteNrqlCondition(conditionID)
	if err != nil {
		return err
	}

	return nil
}

func canUseNerdGraphNrqlAlertConditions(providerConfig *ProviderConfig, conditionType string) bool {
	return providerConfig.hasNerdGraphCredentials() && conditionType != "outlier"
}
