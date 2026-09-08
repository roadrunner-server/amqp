<?php

ini_set("display_errors", "stderr");
require dirname(__DIR__) . "/vendor/autoload.php";

$consumer = new Spiral\RoadRunner\Jobs\Consumer();

while ($task = $consumer->waitTask()) {
    try {
        // RabbitMQ checks delivery acknowledgement timeouts once per minute.
        // https://www.rabbitmq.com/docs/3.13/consumers#acknowledgement-timeout
        sleep(65);
        $task->ack();
    } catch (\Throwable $e) {
        $task->fail($e);
    }
}
