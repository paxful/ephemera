<template>
    <div id="app">
        <b-navbar type="dark" variant="primary" sticky="true">
            <b-container>
                <b-col cols="2" align="left">
                    <img src="./assets/lock.svg" @click="$router.push('/')" width="36">
                </b-col>
                <b-col cols="8" align="center">
                    <img src="./assets/logo.png">
                </b-col>
                <b-col cols="2"></b-col>
            </b-container>
        </b-navbar>
        <router-view></router-view>
    </div>
</template>

<script>
</script>

<style>
</style>
