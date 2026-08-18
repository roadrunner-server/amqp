<?php

ini_set("display_errors", "stderr");
require dirname(__DIR__) . "/vendor/autoload.php";

$consumer = new Spiral\RoadRunner\Jobs\Consumer();

while ($task = $consumer->waitTask()) {
    try {
        // outlives the broker consumer_timeout and rabbit's once a minute
        // timeout sweep, so the delivery is canceled mid-processing
        sleep(65);
        $task->ack();
    } catch (\Throwable $e) {
        $task->fail($e);
    }
}
