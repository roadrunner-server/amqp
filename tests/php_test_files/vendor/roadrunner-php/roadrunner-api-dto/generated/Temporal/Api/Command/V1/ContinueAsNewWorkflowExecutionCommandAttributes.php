<?php
# Generated by the protocol buffer compiler.  DO NOT EDIT!
# source: temporal/api/command/v1/message.proto

namespace Temporal\Api\Command\V1;

use Google\Protobuf\Internal\GPBType;
use Google\Protobuf\Internal\RepeatedField;
use Google\Protobuf\Internal\GPBUtil;

/**
 * Generated from protobuf message <code>temporal.api.command.v1.ContinueAsNewWorkflowExecutionCommandAttributes</code>
 */
class ContinueAsNewWorkflowExecutionCommandAttributes extends \Google\Protobuf\Internal\Message
{
    /**
     * Generated from protobuf field <code>.temporal.api.common.v1.WorkflowType workflow_type = 1;</code>
     */
    protected $workflow_type = null;
    /**
     * Generated from protobuf field <code>.temporal.api.taskqueue.v1.TaskQueue task_queue = 2;</code>
     */
    protected $task_queue = null;
    /**
     * Generated from protobuf field <code>.temporal.api.common.v1.Payloads input = 3;</code>
     */
    protected $input = null;
    /**
     * Timeout of a single workflow run.
     *
     * Generated from protobuf field <code>.google.protobuf.Duration workflow_run_timeout = 4 [(.gogoproto.stdduration) = true];</code>
     */
    protected $workflow_run_timeout = null;
    /**
     * Timeout of a single workflow task.
     *
     * Generated from protobuf field <code>.google.protobuf.Duration workflow_task_timeout = 5 [(.gogoproto.stdduration) = true];</code>
     */
    protected $workflow_task_timeout = null;
    /**
     * How long the workflow start will be delayed - not really a "backoff" in the traditional sense.
     *
     * Generated from protobuf field <code>.google.protobuf.Duration backoff_start_interval = 6 [(.gogoproto.stdduration) = true];</code>
     */
    protected $backoff_start_interval = null;
    /**
     * Generated from protobuf field <code>.temporal.api.common.v1.RetryPolicy retry_policy = 7;</code>
     */
    protected $retry_policy = null;
    /**
     * Should be removed
     *
     * Generated from protobuf field <code>.temporal.api.enums.v1.ContinueAsNewInitiator initiator = 8;</code>
     */
    protected $initiator = 0;
    /**
     * Should be removed
     *
     * Generated from protobuf field <code>.temporal.api.failure.v1.Failure failure = 9;</code>
     */
    protected $failure = null;
    /**
     * Should be removed
     *
     * Generated from protobuf field <code>.temporal.api.common.v1.Payloads last_completion_result = 10;</code>
     */
    protected $last_completion_result = null;
    /**
     * Should be removed. Not necessarily unused but unclear and not exposed by SDKs.
     *
     * Generated from protobuf field <code>string cron_schedule = 11;</code>
     */
    protected $cron_schedule = '';
    /**
     * Generated from protobuf field <code>.temporal.api.common.v1.Header header = 12;</code>
     */
    protected $header = null;
    /**
     * Generated from protobuf field <code>.temporal.api.common.v1.Memo memo = 13;</code>
     */
    protected $memo = null;
    /**
     * Generated from protobuf field <code>.temporal.api.common.v1.SearchAttributes search_attributes = 14;</code>
     */
    protected $search_attributes = null;
    /**
     * If this is set, the workflow executing this command wishes to continue as new using a version
     * compatible with the version that this workflow most recently ran on.
     *
     * Generated from protobuf field <code>bool use_compatible_version = 15;</code>
     */
    protected $use_compatible_version = false;

    /**
     * Constructor.
     *
     * @param array $data {
     *     Optional. Data for populating the Message object.
     *
     *     @type \Temporal\Api\Common\V1\WorkflowType $workflow_type
     *     @type \Temporal\Api\Taskqueue\V1\TaskQueue $task_queue
     *     @type \Temporal\Api\Common\V1\Payloads $input
     *     @type \Google\Protobuf\Duration $workflow_run_timeout
     *           Timeout of a single workflow run.
     *     @type \Google\Protobuf\Duration $workflow_task_timeout
     *           Timeout of a single workflow task.
     *     @type \Google\Protobuf\Duration $backoff_start_interval
     *           How long the workflow start will be delayed - not really a "backoff" in the traditional sense.
     *     @type \Temporal\Api\Common\V1\RetryPolicy $retry_policy
     *     @type int $initiator
     *           Should be removed
     *     @type \Temporal\Api\Failure\V1\Failure $failure
     *           Should be removed
     *     @type \Temporal\Api\Common\V1\Payloads $last_completion_result
     *           Should be removed
     *     @type string $cron_schedule
     *           Should be removed. Not necessarily unused but unclear and not exposed by SDKs.
     *     @type \Temporal\Api\Common\V1\Header $header
     *     @type \Temporal\Api\Common\V1\Memo $memo
     *     @type \Temporal\Api\Common\V1\SearchAttributes $search_attributes
     *     @type bool $use_compatible_version
     *           If this is set, the workflow executing this command wishes to continue as new using a version
     *           compatible with the version that this workflow most recently ran on.
     * }
     */
    public function __construct($data = NULL) {
        \GPBMetadata\Temporal\Api\Command\V1\Message::initOnce();
        parent::__construct($data);
    }

    /**
     * Generated from protobuf field <code>.temporal.api.common.v1.WorkflowType workflow_type = 1;</code>
     * @return \Temporal\Api\Common\V1\WorkflowType|null
     */
    public function getWorkflowType()
    {
        return $this->workflow_type;
    }

    public function hasWorkflowType()
    {
        return isset($this->workflow_type);
    }

    public function clearWorkflowType()
    {
        unset($this->workflow_type);
    }

    /**
     * Generated from protobuf field <code>.temporal.api.common.v1.WorkflowType workflow_type = 1;</code>
     * @param \Temporal\Api\Common\V1\WorkflowType $var
     * @return $this
     */
    public function setWorkflowType($var)
    {
        GPBUtil::checkMessage($var, \Temporal\Api\Common\V1\WorkflowType::class);
        $this->workflow_type = $var;

        return $this;
    }

    /**
     * Generated from protobuf field <code>.temporal.api.taskqueue.v1.TaskQueue task_queue = 2;</code>
     * @return \Temporal\Api\Taskqueue\V1\TaskQueue|null
     */
    public function getTaskQueue()
    {
        return $this->task_queue;
    }

    public function hasTaskQueue()
    {
        return isset($this->task_queue);
    }

    public function clearTaskQueue()
    {
        unset($this->task_queue);
    }

    /**
     * Generated from protobuf field <code>.temporal.api.taskqueue.v1.TaskQueue task_queue = 2;</code>
     * @param \Temporal\Api\Taskqueue\V1\TaskQueue $var
     * @return $this
     */
    public function setTaskQueue($var)
    {
        GPBUtil::checkMessage($var, \Temporal\Api\Taskqueue\V1\TaskQueue::class);
        $this->task_queue = $var;

        return $this;
    }

    /**
     * Generated from protobuf field <code>.temporal.api.common.v1.Payloads input = 3;</code>
     * @return \Temporal\Api\Common\V1\Payloads|null
     */
    public function getInput()
    {
        return $this->input;
    }

    public function hasInput()
    {
        return isset($this->input);
    }

    public function clearInput()
    {
        unset($this->input);
    }

    /**
     * Generated from protobuf field <code>.temporal.api.common.v1.Payloads input = 3;</code>
     * @param \Temporal\Api\Common\V1\Payloads $var
     * @return $this
     */
    public function setInput($var)
    {
        GPBUtil::checkMessage($var, \Temporal\Api\Common\V1\Payloads::class);
        $this->input = $var;

        return $this;
    }

    /**
     * Timeout of a single workflow run.
     *
     * Generated from protobuf field <code>.google.protobuf.Duration workflow_run_timeout = 4 [(.gogoproto.stdduration) = true];</code>
     * @return \Google\Protobuf\Duration|null
     */
    public function getWorkflowRunTimeout()
    {
        return $this->workflow_run_timeout;
    }

    public function hasWorkflowRunTimeout()
    {
        return isset($this->workflow_run_timeout);
    }

    public function clearWorkflowRunTimeout()
    {
        unset($this->workflow_run_timeout);
    }

    /**
     * Timeout of a single workflow run.
     *
     * Generated from protobuf field <code>.google.protobuf.Duration workflow_run_timeout = 4 [(.gogoproto.stdduration) = true];</code>
     * @param \Google\Protobuf\Duration $var
     * @return $this
     */
    public function setWorkflowRunTimeout($var)
    {
        GPBUtil::checkMessage($var, \Google\Protobuf\Duration::class);
        $this->workflow_run_timeout = $var;

        return $this;
    }

    /**
     * Timeout of a single workflow task.
     *
     * Generated from protobuf field <code>.google.protobuf.Duration workflow_task_timeout = 5 [(.gogoproto.stdduration) = true];</code>
     * @return \Google\Protobuf\Duration|null
     */
    public function getWorkflowTaskTimeout()
    {
        return $this->workflow_task_timeout;
    }

    public function hasWorkflowTaskTimeout()
    {
        return isset($this->workflow_task_timeout);
    }

    public function clearWorkflowTaskTimeout()
    {
        unset($this->workflow_task_timeout);
    }

    /**
     * Timeout of a single workflow task.
     *
     * Generated from protobuf field <code>.google.protobuf.Duration workflow_task_timeout = 5 [(.gogoproto.stdduration) = true];</code>
     * @param \Google\Protobuf\Duration $var
     * @return $this
     */
    public function setWorkflowTaskTimeout($var)
    {
        GPBUtil::checkMessage($var, \Google\Protobuf\Duration::class);
        $this->workflow_task_timeout = $var;

        return $this;
    }

    /**
     * How long the workflow start will be delayed - not really a "backoff" in the traditional sense.
     *
     * Generated from protobuf field <code>.google.protobuf.Duration backoff_start_interval = 6 [(.gogoproto.stdduration) = true];</code>
     * @return \Google\Protobuf\Duration|null
     */
    public function getBackoffStartInterval()
    {
        return $this->backoff_start_interval;
    }

    public function hasBackoffStartInterval()
    {
        return isset($this->backoff_start_interval);
    }

    public function clearBackoffStartInterval()
    {
        unset($this->backoff_start_interval);
    }

    /**
     * How long the workflow start will be delayed - not really a "backoff" in the traditional sense.
     *
     * Generated from protobuf field <code>.google.protobuf.Duration backoff_start_interval = 6 [(.gogoproto.stdduration) = true];</code>
     * @param \Google\Protobuf\Duration $var
     * @return $this
     */
    public function setBackoffStartInterval($var)
    {
        GPBUtil::checkMessage($var, \Google\Protobuf\Duration::class);
        $this->backoff_start_interval = $var;

        return $this;
    }

    /**
     * Generated from protobuf field <code>.temporal.api.common.v1.RetryPolicy retry_policy = 7;</code>
     * @return \Temporal\Api\Common\V1\RetryPolicy|null
     */
    public function getRetryPolicy()
    {
        return $this->retry_policy;
    }

    public function hasRetryPolicy()
    {
        return isset($this->retry_policy);
    }

    public function clearRetryPolicy()
    {
        unset($this->retry_policy);
    }

    /**
     * Generated from protobuf field <code>.temporal.api.common.v1.RetryPolicy retry_policy = 7;</code>
     * @param \Temporal\Api\Common\V1\RetryPolicy $var
     * @return $this
     */
    public function setRetryPolicy($var)
    {
        GPBUtil::checkMessage($var, \Temporal\Api\Common\V1\RetryPolicy::class);
        $this->retry_policy = $var;

        return $this;
    }

    /**
     * Should be removed
     *
     * Generated from protobuf field <code>.temporal.api.enums.v1.ContinueAsNewInitiator initiator = 8;</code>
     * @return int
     */
    public function getInitiator()
    {
        return $this->initiator;
    }

    /**
     * Should be removed
     *
     * Generated from protobuf field <code>.temporal.api.enums.v1.ContinueAsNewInitiator initiator = 8;</code>
     * @param int $var
     * @return $this
     */
    public function setInitiator($var)
    {
        GPBUtil::checkEnum($var, \Temporal\Api\Enums\V1\ContinueAsNewInitiator::class);
        $this->initiator = $var;

        return $this;
    }

    /**
     * Should be removed
     *
     * Generated from protobuf field <code>.temporal.api.failure.v1.Failure failure = 9;</code>
     * @return \Temporal\Api\Failure\V1\Failure|null
     */
    public function getFailure()
    {
        return $this->failure;
    }

    public function hasFailure()
    {
        return isset($this->failure);
    }

    public function clearFailure()
    {
        unset($this->failure);
    }

    /**
     * Should be removed
     *
     * Generated from protobuf field <code>.temporal.api.failure.v1.Failure failure = 9;</code>
     * @param \Temporal\Api\Failure\V1\Failure $var
     * @return $this
     */
    public function setFailure($var)
    {
        GPBUtil::checkMessage($var, \Temporal\Api\Failure\V1\Failure::class);
        $this->failure = $var;

        return $this;
    }

    /**
     * Should be removed
     *
     * Generated from protobuf field <code>.temporal.api.common.v1.Payloads last_completion_result = 10;</code>
     * @return \Temporal\Api\Common\V1\Payloads|null
     */
    public function getLastCompletionResult()
    {
        return $this->last_completion_result;
    }

    public function hasLastCompletionResult()
    {
        return isset($this->last_completion_result);
    }

    public function clearLastCompletionResult()
    {
        unset($this->last_completion_result);
    }

    /**
     * Should be removed
     *
     * Generated from protobuf field <code>.temporal.api.common.v1.Payloads last_completion_result = 10;</code>
     * @param \Temporal\Api\Common\V1\Payloads $var
     * @return $this
     */
    public function setLastCompletionResult($var)
    {
        GPBUtil::checkMessage($var, \Temporal\Api\Common\V1\Payloads::class);
        $this->last_completion_result = $var;

        return $this;
    }

    /**
     * Should be removed. Not necessarily unused but unclear and not exposed by SDKs.
     *
     * Generated from protobuf field <code>string cron_schedule = 11;</code>
     * @return string
     */
    public function getCronSchedule()
    {
        return $this->cron_schedule;
    }

    /**
     * Should be removed. Not necessarily unused but unclear and not exposed by SDKs.
     *
     * Generated from protobuf field <code>string cron_schedule = 11;</code>
     * @param string $var
     * @return $this
     */
    public function setCronSchedule($var)
    {
        GPBUtil::checkString($var, True);
        $this->cron_schedule = $var;

        return $this;
    }

    /**
     * Generated from protobuf field <code>.temporal.api.common.v1.Header header = 12;</code>
     * @return \Temporal\Api\Common\V1\Header|null
     */
    public function getHeader()
    {
        return $this->header;
    }

    public function hasHeader()
    {
        return isset($this->header);
    }

    public function clearHeader()
    {
        unset($this->header);
    }

    /**
     * Generated from protobuf field <code>.temporal.api.common.v1.Header header = 12;</code>
     * @param \Temporal\Api\Common\V1\Header $var
     * @return $this
     */
    public function setHeader($var)
    {
        GPBUtil::checkMessage($var, \Temporal\Api\Common\V1\Header::class);
        $this->header = $var;

        return $this;
    }

    /**
     * Generated from protobuf field <code>.temporal.api.common.v1.Memo memo = 13;</code>
     * @return \Temporal\Api\Common\V1\Memo|null
     */
    public function getMemo()
    {
        return $this->memo;
    }

    public function hasMemo()
    {
        return isset($this->memo);
    }

    public function clearMemo()
    {
        unset($this->memo);
    }

    /**
     * Generated from protobuf field <code>.temporal.api.common.v1.Memo memo = 13;</code>
     * @param \Temporal\Api\Common\V1\Memo $var
     * @return $this
     */
    public function setMemo($var)
    {
        GPBUtil::checkMessage($var, \Temporal\Api\Common\V1\Memo::class);
        $this->memo = $var;

        return $this;
    }

    /**
     * Generated from protobuf field <code>.temporal.api.common.v1.SearchAttributes search_attributes = 14;</code>
     * @return \Temporal\Api\Common\V1\SearchAttributes|null
     */
    public function getSearchAttributes()
    {
        return $this->search_attributes;
    }

    public function hasSearchAttributes()
    {
        return isset($this->search_attributes);
    }

    public function clearSearchAttributes()
    {
        unset($this->search_attributes);
    }

    /**
     * Generated from protobuf field <code>.temporal.api.common.v1.SearchAttributes search_attributes = 14;</code>
     * @param \Temporal\Api\Common\V1\SearchAttributes $var
     * @return $this
     */
    public function setSearchAttributes($var)
    {
        GPBUtil::checkMessage($var, \Temporal\Api\Common\V1\SearchAttributes::class);
        $this->search_attributes = $var;

        return $this;
    }

    /**
     * If this is set, the workflow executing this command wishes to continue as new using a version
     * compatible with the version that this workflow most recently ran on.
     *
     * Generated from protobuf field <code>bool use_compatible_version = 15;</code>
     * @return bool
     */
    public function getUseCompatibleVersion()
    {
        return $this->use_compatible_version;
    }

    /**
     * If this is set, the workflow executing this command wishes to continue as new using a version
     * compatible with the version that this workflow most recently ran on.
     *
     * Generated from protobuf field <code>bool use_compatible_version = 15;</code>
     * @param bool $var
     * @return $this
     */
    public function setUseCompatibleVersion($var)
    {
        GPBUtil::checkBool($var);
        $this->use_compatible_version = $var;

        return $this;
    }

}
