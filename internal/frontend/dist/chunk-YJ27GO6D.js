import{a as Me,c as bt,f as N,g as pe,h as fe,j as I,k as j,l as _e,n as ge,o as vt,p as yt,q as H,s as be,v as K}from"./chunk-RZQOW22N.js";import{$ as Ge,$a as b,A as z,Aa as U,Ab as ke,Bb as P,Cb as tt,Dc as Te,Ec as ct,F,Fc as L,Ga as g,Ha as D,Hc as me,Ia as p,Ja as $e,K as ie,Ka as T,La as B,M as qe,Nb as re,Nc as Ee,Oa as Ke,Oc as he,Pb as xe,Qc as ue,Rc as Se,S as x,T as C,Ua as A,V as h,Va as Ye,Wa as Xe,X as o,_a as De,a as m,aa as We,ab as _,ad as dt,b as je,bb as Je,bd as mt,cc as le,da as u,dd as ht,ea as q,ec as M,fc as ce,fd as ut,gc as we,h as c,ha as G,hb as et,hc as it,hd as pt,ia as w,ib as Z,id as ft,j as Ce,jc as nt,kb as E,la as Ue,lb as Ae,ld as _t,m as He,ma as Ze,mc as at,md as gt,na as ne,o as Ve,oa as ae,pa as y,pb as se,pc as ot,qb as Q,qc as st,rb as $,rc as rt,sc as lt,tc as de,va as f,wc as k,x as te,xa as W,xb as O,ya as oe,z as ze,za as Qe,zb as S}from"./chunk-SZWSLCWU.js";function Vt(n,s){}var R=class{viewContainerRef;injector;id;role="dialog";panelClass="";hasBackdrop=!0;backdropClass="";disableClose=!1;closePredicate;width="";height="";minWidth;minHeight;maxWidth;maxHeight;positionStrategy;data=null;direction;ariaDescribedBy=null;ariaLabelledBy=null;ariaLabel=null;ariaModal=!1;autoFocus="first-tabbable";restoreFocus=!0;scrollStrategy;closeOnNavigation=!0;closeOnDestroy=!0;closeOnOverlayDetachments=!0;disableAnimations=!1;providers;container;templateContext};var Re=(()=>{class n extends fe{_elementRef=o(y);_focusTrapFactory=o(st);_config;_interactivityChecker=o(ot);_ngZone=o(w);_focusMonitor=o(it);_renderer=o(U);_changeDetectorRef=o(re);_injector=o(u);_platform=o(M);_document=o(q);_portalOutlet;_focusTrapped=new c;_focusTrap=null;_elementFocusedBeforeDialogWasOpened=null;_closeInteractionType=null;_ariaLabelledByQueue=[];_isDestroyed=!1;constructor(){super(),this._config=o(R,{optional:!0})||new R,this._config.ariaLabelledBy&&this._ariaLabelledByQueue.push(this._config.ariaLabelledBy)}_addAriaLabelledBy(e){this._ariaLabelledByQueue.push(e),this._changeDetectorRef.markForCheck()}_removeAriaLabelledBy(e){let t=this._ariaLabelledByQueue.indexOf(e);t>-1&&(this._ariaLabelledByQueue.splice(t,1),this._changeDetectorRef.markForCheck())}_contentAttached(){this._initializeFocusTrap(),this._captureInitialFocus()}_captureInitialFocus(){this._trapFocus()}ngOnDestroy(){this._focusTrapped.complete(),this._isDestroyed=!0,this._restoreFocus()}attachComponentPortal(e){this._portalOutlet.hasAttached();let t=this._portalOutlet.attachComponentPortal(e);return this._contentAttached(),t}attachTemplatePortal(e){this._portalOutlet.hasAttached();let t=this._portalOutlet.attachTemplatePortal(e);return this._contentAttached(),t}attachDomPortal=e=>{this._portalOutlet.hasAttached();let t=this._portalOutlet.attachDomPortal(e);return this._contentAttached(),t};_recaptureFocus(){this._containsFocus()||this._trapFocus()}_forceFocus(e,t){this._interactivityChecker.isFocusable(e)||(e.tabIndex=-1,this._ngZone.runOutsideAngular(()=>{let i=()=>{a(),r(),e.removeAttribute("tabindex")},a=this._renderer.listen(e,"blur",i),r=this._renderer.listen(e,"mousedown",i)})),e.focus(t)}_focusByCssSelector(e,t){let i=this._elementRef.nativeElement.querySelector(e);i&&this._forceFocus(i,t)}_trapFocus(e){this._isDestroyed||W(()=>{let t=this._elementRef.nativeElement;switch(this._config.autoFocus){case!1:case"dialog":this._containsFocus()||t.focus(e);break;case!0:case"first-tabbable":this._focusTrap?.focusInitialElement(e)||this._focusDialogContainer(e);break;case"first-heading":this._focusByCssSelector('h1, h2, h3, h4, h5, h6, [role="heading"]',e);break;default:this._focusByCssSelector(this._config.autoFocus,e);break}this._focusTrapped.next()},{injector:this._injector})}_restoreFocus(){let e=this._config.restoreFocus,t=null;if(typeof e=="string"?t=this._document.querySelector(e):typeof e=="boolean"?t=e?this._elementFocusedBeforeDialogWasOpened:null:e&&(t=e),this._config.restoreFocus&&t&&typeof t.focus=="function"){let i=le(),a=this._elementRef.nativeElement;(!i||i===this._document.body||i===a||a.contains(i))&&(this._focusMonitor?(this._focusMonitor.focusVia(t,this._closeInteractionType),this._closeInteractionType=null):t.focus())}this._focusTrap&&this._focusTrap.destroy()}_focusDialogContainer(e){this._elementRef.nativeElement.focus?.(e)}_containsFocus(){let e=this._elementRef.nativeElement,t=le();return e===t||e.contains(t)}_initializeFocusTrap(){this._platform.isBrowser&&(this._focusTrap=this._focusTrapFactory.create(this._elementRef.nativeElement),this._document&&(this._elementFocusedBeforeDialogWasOpened=le()))}static \u0275fac=function(t){return new(t||n)};static \u0275cmp=g({type:n,selectors:[["cdk-dialog-container"]],viewQuery:function(t,i){if(t&1&&se(I,7),t&2){let a;Q(a=$())&&(i._portalOutlet=a.first)}},hostAttrs:["tabindex","-1",1,"cdk-dialog-container"],hostVars:6,hostBindings:function(t,i){t&2&&A("id",i._config.id||null)("role",i._config.role)("aria-modal",i._config.ariaModal)("aria-labelledby",i._config.ariaLabel?null:i._ariaLabelledByQueue[0])("aria-label",i._config.ariaLabel)("aria-describedby",i._config.ariaDescribedBy||null)},features:[T],decls:1,vars:0,consts:[["cdkPortalOutlet",""]],template:function(t,i){t&1&&B(0,Vt,0,0,"ng-template",0)},dependencies:[I],styles:[`.cdk-dialog-container {
  display: block;
  width: 100%;
  height: 100%;
  min-height: inherit;
  max-height: inherit;
}
`],encapsulation:2})}return n})(),Y=class{overlayRef;config;componentInstance=null;componentRef=null;containerInstance;disableClose;closed=new c;backdropClick;keydownEvents;outsidePointerEvents;id;_detachSubscription;constructor(s,e){this.overlayRef=s,this.config=e,this.disableClose=e.disableClose,this.backdropClick=s.backdropClick(),this.keydownEvents=s.keydownEvents(),this.outsidePointerEvents=s.outsidePointerEvents(),this.id=e.id,this.keydownEvents.subscribe(t=>{t.keyCode===27&&!this.disableClose&&!de(t)&&(t.preventDefault(),this.close(void 0,{focusOrigin:"keyboard"}))}),this.backdropClick.subscribe(()=>{!this.disableClose&&this._canClose()?this.close(void 0,{focusOrigin:"mouse"}):this.containerInstance._recaptureFocus?.()}),this._detachSubscription=s.detachments().subscribe(()=>{e.closeOnOverlayDetachments!==!1&&this.close()})}close(s,e){if(this._canClose(s)){let t=this.closed;this.containerInstance._closeInteractionType=e?.focusOrigin||"program",this._detachSubscription.unsubscribe(),this.overlayRef.dispose(),t.next(s),t.complete(),this.componentInstance=this.containerInstance=null}}updatePosition(){return this.overlayRef.updatePosition(),this}updateSize(s="",e=""){return this.overlayRef.updateSize({width:s,height:e}),this}addPanelClass(s){return this.overlayRef.addPanelClass(s),this}removePanelClass(s){return this.overlayRef.removePanelClass(s),this}_canClose(s){let e=this.config;return!!this.containerInstance&&(!e.closePredicate||e.closePredicate(s,e,this.componentInstance))}},zt=new h("DialogScrollStrategy",{providedIn:"root",factory:()=>{let n=o(u);return()=>_e(n)}}),qt=new h("DialogData"),Gt=new h("DefaultDialogConfig");function Wt(n){let s=Ue(n),e=new G;return{valueSignal:s,get value(){return s()},change:e,ngOnDestroy(){e.complete()}}}var Oe=(()=>{class n{_injector=o(u);_defaultOptions=o(Gt,{optional:!0});_parentDialog=o(n,{optional:!0,skipSelf:!0});_overlayContainer=o(vt);_idGenerator=o(k);_openDialogsAtThisLevel=[];_afterAllClosedAtThisLevel=new c;_afterOpenedAtThisLevel=new c;_ariaHiddenElements=new Map;_scrollStrategy=o(zt);get openDialogs(){return this._parentDialog?this._parentDialog.openDialogs:this._openDialogsAtThisLevel}get afterOpened(){return this._parentDialog?this._parentDialog.afterOpened:this._afterOpenedAtThisLevel}afterAllClosed=te(()=>this.openDialogs.length?this._getAfterAllClosed():this._getAfterAllClosed().pipe(ie(void 0)));constructor(){}open(e,t){let i=this._defaultOptions||new R;t=m(m({},i),t),t.id=t.id||this._idGenerator.getId("cdk-dialog-"),t.id&&this.getDialogById(t.id);let a=this._getOverlayConfig(t),r=be(this._injector,a),l=new Y(r,t),d=this._attachContainer(r,l,t);if(l.containerInstance=d,!this.openDialogs.length){let v=this._overlayContainer.getContainerElement();d._focusTrapped?d._focusTrapped.pipe(F(1)).subscribe(()=>{this._hideNonDialogContentFromAssistiveTechnology(v)}):this._hideNonDialogContentFromAssistiveTechnology(v)}return this._attachDialogContent(e,l,d,t),this.openDialogs.push(l),l.closed.subscribe(()=>this._removeOpenDialog(l,!0)),this.afterOpened.next(l),l}closeAll(){Ie(this.openDialogs,e=>e.close())}getDialogById(e){return this.openDialogs.find(t=>t.id===e)}ngOnDestroy(){Ie(this._openDialogsAtThisLevel,e=>{e.config.closeOnDestroy===!1&&this._removeOpenDialog(e,!1)}),Ie(this._openDialogsAtThisLevel,e=>e.close()),this._afterAllClosedAtThisLevel.complete(),this._afterOpenedAtThisLevel.complete(),this._openDialogsAtThisLevel=[]}_getOverlayConfig(e){let t=new ge({positionStrategy:e.positionStrategy||H().centerHorizontally().centerVertically(),scrollStrategy:e.scrollStrategy||this._scrollStrategy(),panelClass:e.panelClass,hasBackdrop:e.hasBackdrop,direction:e.direction,minWidth:e.minWidth,minHeight:e.minHeight,maxWidth:e.maxWidth,maxHeight:e.maxHeight,width:e.width,height:e.height,disposeOnNavigation:e.closeOnNavigation,disableAnimations:e.disableAnimations});return e.backdropClass&&(t.backdropClass=e.backdropClass),t}_attachContainer(e,t,i){let a=i.injector||i.viewContainerRef?.injector,r=[{provide:R,useValue:i},{provide:Y,useValue:t},{provide:yt,useValue:e}],l;i.container?typeof i.container=="function"?l=i.container:(l=i.container.type,r.push(...i.container.providers(i))):l=Re;let d=new N(l,i.viewContainerRef,u.create({parent:a||this._injector,providers:r}));return e.attach(d).instance}_attachDialogContent(e,t,i,a){if(e instanceof oe){let r=this._createInjector(a,t,i,void 0),l={$implicit:a.data,dialogRef:t};a.templateContext&&(l=m(m({},l),typeof a.templateContext=="function"?a.templateContext():a.templateContext)),i.attachTemplatePortal(new pe(e,null,l,r))}else{let r=this._createInjector(a,t,i,this._injector),l=i.attachComponentPortal(new N(e,a.viewContainerRef,r));t.componentRef=l,t.componentInstance=l.instance}}_createInjector(e,t,i,a){let r=e.injector||e.viewContainerRef?.injector,l=[{provide:qt,useValue:e.data},{provide:Y,useValue:t}];return e.providers&&(typeof e.providers=="function"?l.push(...e.providers(t,e,i)):l.push(...e.providers)),e.direction&&(!r||!r.get(Ee,null,{optional:!0}))&&l.push({provide:Ee,useValue:Wt(e.direction)}),u.create({parent:r||a,providers:l})}_removeOpenDialog(e,t){let i=this.openDialogs.indexOf(e);i>-1&&(this.openDialogs.splice(i,1),this.openDialogs.length||(this._ariaHiddenElements.forEach((a,r)=>{a?r.setAttribute("aria-hidden",a):r.removeAttribute("aria-hidden")}),this._ariaHiddenElements.clear(),t&&this._getAfterAllClosed().next()))}_hideNonDialogContentFromAssistiveTechnology(e){if(e.parentElement){let t=e.parentElement.children;for(let i=t.length-1;i>-1;i--){let a=t[i];a!==e&&a.nodeName!=="SCRIPT"&&a.nodeName!=="STYLE"&&!a.hasAttribute("aria-live")&&!a.hasAttribute("popover")&&(this._ariaHiddenElements.set(a,a.getAttribute("aria-hidden")),a.setAttribute("aria-hidden","true"))}}}_getAfterAllClosed(){let e=this._parentDialog;return e?e._getAfterAllClosed():this._afterAllClosedAtThisLevel}static \u0275fac=function(t){return new(t||n)};static \u0275prov=x({token:n,factory:n.\u0275fac,providedIn:"root"})}return n})();function Ie(n,s){let e=n.length;for(;e--;)s(n[e])}var Dt=(()=>{class n{static \u0275fac=function(t){return new(t||n)};static \u0275mod=D({type:n});static \u0275inj=C({providers:[Oe],imports:[K,j,lt,j]})}return n})();function Ut(n,s){}var ye=class{viewContainerRef;injector;id;role="dialog";panelClass="";hasBackdrop=!0;backdropClass="";disableClose=!1;closePredicate;width="";height="";minWidth;minHeight;maxWidth;maxHeight;position;data=null;direction;ariaDescribedBy=null;ariaLabelledBy=null;ariaLabel=null;ariaModal=!1;autoFocus="first-tabbable";restoreFocus=!0;delayFocusTrap=!0;scrollStrategy;closeOnNavigation=!0;enterAnimationDuration;exitAnimationDuration},Fe="mdc-dialog--open",At="mdc-dialog--opening",kt="mdc-dialog--closing",Zt=150,Qt=75,$t=(()=>{class n extends Re{_animationStateChanged=new G;_animationsEnabled=!L();_actionSectionCount=0;_hostElement=this._elementRef.nativeElement;_enterAnimationDuration=this._animationsEnabled?wt(this._config.enterAnimationDuration)??Zt:0;_exitAnimationDuration=this._animationsEnabled?wt(this._config.exitAnimationDuration)??Qt:0;_animationTimer=null;_contentAttached(){super._contentAttached(),this._startOpenAnimation()}_startOpenAnimation(){this._animationStateChanged.emit({state:"opening",totalTime:this._enterAnimationDuration}),this._animationsEnabled?(this._hostElement.style.setProperty(xt,`${this._enterAnimationDuration}ms`),this._requestAnimationFrame(()=>this._hostElement.classList.add(At,Fe)),this._waitForAnimationToComplete(this._enterAnimationDuration,this._finishDialogOpen)):(this._hostElement.classList.add(Fe),Promise.resolve().then(()=>this._finishDialogOpen()))}_startExitAnimation(){this._animationStateChanged.emit({state:"closing",totalTime:this._exitAnimationDuration}),this._hostElement.classList.remove(Fe),this._animationsEnabled?(this._hostElement.style.setProperty(xt,`${this._exitAnimationDuration}ms`),this._requestAnimationFrame(()=>this._hostElement.classList.add(kt)),this._waitForAnimationToComplete(this._exitAnimationDuration,this._finishDialogClose)):Promise.resolve().then(()=>this._finishDialogClose())}_updateActionSectionCount(e){this._actionSectionCount+=e,this._changeDetectorRef.markForCheck()}_finishDialogOpen=()=>{this._clearAnimationClasses(),this._openAnimationDone(this._enterAnimationDuration)};_finishDialogClose=()=>{this._clearAnimationClasses(),this._animationStateChanged.emit({state:"closed",totalTime:this._exitAnimationDuration})};_clearAnimationClasses(){this._hostElement.classList.remove(At,kt)}_waitForAnimationToComplete(e,t){this._animationTimer!==null&&clearTimeout(this._animationTimer),this._animationTimer=setTimeout(t,e)}_requestAnimationFrame(e){this._ngZone.runOutsideAngular(()=>{typeof requestAnimationFrame=="function"?requestAnimationFrame(e):e()})}_captureInitialFocus(){this._config.delayFocusTrap||this._trapFocus()}_openAnimationDone(e){this._config.delayFocusTrap&&this._trapFocus(),this._animationStateChanged.next({state:"opened",totalTime:e})}ngOnDestroy(){super.ngOnDestroy(),this._animationTimer!==null&&clearTimeout(this._animationTimer)}attachComponentPortal(e){let t=super.attachComponentPortal(e);return t.location.nativeElement.classList.add("mat-mdc-dialog-component-host"),t}static \u0275fac=(()=>{let e;return function(i){return(e||(e=ae(n)))(i||n)}})();static \u0275cmp=g({type:n,selectors:[["mat-dialog-container"]],hostAttrs:["tabindex","-1",1,"mat-mdc-dialog-container","mdc-dialog"],hostVars:10,hostBindings:function(t,i){t&2&&(Z("id",i._config.id),A("aria-modal",i._config.ariaModal)("role",i._config.role)("aria-labelledby",i._config.ariaLabel?null:i._ariaLabelledByQueue[0])("aria-label",i._config.ariaLabel)("aria-describedby",i._config.ariaDescribedBy||null),O("_mat-animation-noopable",!i._animationsEnabled)("mat-mdc-dialog-container-with-actions",i._actionSectionCount>0))},features:[T],decls:3,vars:0,consts:[[1,"mat-mdc-dialog-inner-container","mdc-dialog__container"],[1,"mat-mdc-dialog-surface","mdc-dialog__surface"],["cdkPortalOutlet",""]],template:function(t,i){t&1&&(b(0,"div",0)(1,"div",1),B(2,Ut,0,0,"ng-template",2),_()())},dependencies:[I],styles:[`.mat-mdc-dialog-container {
  width: 100%;
  height: 100%;
  display: block;
  box-sizing: border-box;
  max-height: inherit;
  min-height: inherit;
  min-width: inherit;
  max-width: inherit;
  outline: 0;
}

.cdk-overlay-pane.mat-mdc-dialog-panel {
  max-width: var(--mat-dialog-container-max-width, 560px);
  min-width: var(--mat-dialog-container-min-width, 280px);
}
@media (max-width: 599px) {
  .cdk-overlay-pane.mat-mdc-dialog-panel {
    max-width: var(--mat-dialog-container-small-max-width, calc(100vw - 32px));
  }
}

.mat-mdc-dialog-inner-container {
  display: flex;
  flex-direction: row;
  align-items: center;
  justify-content: space-around;
  box-sizing: border-box;
  height: 100%;
  opacity: 0;
  transition: opacity linear var(--mat-dialog-transition-duration, 0ms);
  max-height: inherit;
  min-height: inherit;
  min-width: inherit;
  max-width: inherit;
}
.mdc-dialog--closing .mat-mdc-dialog-inner-container {
  transition: opacity 75ms linear;
  transform: none;
}
.mdc-dialog--open .mat-mdc-dialog-inner-container {
  opacity: 1;
}
._mat-animation-noopable .mat-mdc-dialog-inner-container {
  transition: none;
}

.mat-mdc-dialog-surface {
  display: flex;
  flex-direction: column;
  flex-grow: 0;
  flex-shrink: 0;
  box-sizing: border-box;
  width: 100%;
  height: 100%;
  position: relative;
  overflow-y: auto;
  outline: 0;
  transform: scale(0.8);
  transition: transform var(--mat-dialog-transition-duration, 0ms) cubic-bezier(0, 0, 0.2, 1);
  max-height: inherit;
  min-height: inherit;
  min-width: inherit;
  max-width: inherit;
  box-shadow: var(--mat-dialog-container-elevation-shadow, none);
  border-radius: var(--mat-dialog-container-shape, var(--mat-sys-corner-extra-large, 4px));
  background-color: var(--mat-dialog-container-color, var(--mat-sys-surface, white));
}
[dir=rtl] .mat-mdc-dialog-surface {
  text-align: right;
}
.mdc-dialog--open .mat-mdc-dialog-surface, .mdc-dialog--closing .mat-mdc-dialog-surface {
  transform: none;
}
._mat-animation-noopable .mat-mdc-dialog-surface {
  transition: none;
}
.mat-mdc-dialog-surface::before {
  position: absolute;
  box-sizing: border-box;
  width: 100%;
  height: 100%;
  top: 0;
  left: 0;
  border: 2px solid transparent;
  border-radius: inherit;
  content: "";
  pointer-events: none;
}

.mat-mdc-dialog-title {
  display: block;
  position: relative;
  flex-shrink: 0;
  box-sizing: border-box;
  margin: 0 0 1px;
  padding: var(--mat-dialog-headline-padding, 6px 24px 13px);
}
.mat-mdc-dialog-title::before {
  display: inline-block;
  width: 0;
  height: 40px;
  content: "";
  vertical-align: 0;
}
[dir=rtl] .mat-mdc-dialog-title {
  text-align: right;
}
.mat-mdc-dialog-container .mat-mdc-dialog-title {
  color: var(--mat-dialog-subhead-color, var(--mat-sys-on-surface, rgba(0, 0, 0, 0.87)));
  font-family: var(--mat-dialog-subhead-font, var(--mat-sys-headline-small-font, inherit));
  line-height: var(--mat-dialog-subhead-line-height, var(--mat-sys-headline-small-line-height, 1.5rem));
  font-size: var(--mat-dialog-subhead-size, var(--mat-sys-headline-small-size, 1rem));
  font-weight: var(--mat-dialog-subhead-weight, var(--mat-sys-headline-small-weight, 400));
  letter-spacing: var(--mat-dialog-subhead-tracking, var(--mat-sys-headline-small-tracking, 0.03125em));
}

.mat-mdc-dialog-content {
  display: block;
  flex-grow: 1;
  box-sizing: border-box;
  margin: 0;
  overflow: auto;
  max-height: 65vh;
}
.mat-mdc-dialog-content > :first-child {
  margin-top: 0;
}
.mat-mdc-dialog-content > :last-child {
  margin-bottom: 0;
}
.mat-mdc-dialog-container .mat-mdc-dialog-content {
  color: var(--mat-dialog-supporting-text-color, var(--mat-sys-on-surface-variant, rgba(0, 0, 0, 0.6)));
  font-family: var(--mat-dialog-supporting-text-font, var(--mat-sys-body-medium-font, inherit));
  line-height: var(--mat-dialog-supporting-text-line-height, var(--mat-sys-body-medium-line-height, 1.5rem));
  font-size: var(--mat-dialog-supporting-text-size, var(--mat-sys-body-medium-size, 1rem));
  font-weight: var(--mat-dialog-supporting-text-weight, var(--mat-sys-body-medium-weight, 400));
  letter-spacing: var(--mat-dialog-supporting-text-tracking, var(--mat-sys-body-medium-tracking, 0.03125em));
}
.mat-mdc-dialog-container .mat-mdc-dialog-content {
  padding: var(--mat-dialog-content-padding, 20px 24px);
}
.mat-mdc-dialog-container-with-actions .mat-mdc-dialog-content {
  padding: var(--mat-dialog-with-actions-content-padding, 20px 24px 0);
}
.mat-mdc-dialog-container .mat-mdc-dialog-title + .mat-mdc-dialog-content {
  padding-top: 0;
}

.mat-mdc-dialog-actions {
  display: flex;
  position: relative;
  flex-shrink: 0;
  flex-wrap: wrap;
  align-items: center;
  box-sizing: border-box;
  min-height: 52px;
  margin: 0;
  border-top: 1px solid transparent;
  padding: var(--mat-dialog-actions-padding, 16px 24px);
  justify-content: var(--mat-dialog-actions-alignment, flex-end);
}
@media (forced-colors: active) {
  .mat-mdc-dialog-actions {
    border-top-color: CanvasText;
  }
}
.mat-mdc-dialog-actions.mat-mdc-dialog-actions-align-start, .mat-mdc-dialog-actions[align=start] {
  justify-content: start;
}
.mat-mdc-dialog-actions.mat-mdc-dialog-actions-align-center, .mat-mdc-dialog-actions[align=center] {
  justify-content: center;
}
.mat-mdc-dialog-actions.mat-mdc-dialog-actions-align-end, .mat-mdc-dialog-actions[align=end] {
  justify-content: flex-end;
}
.mat-mdc-dialog-actions .mat-button-base + .mat-button-base,
.mat-mdc-dialog-actions .mat-mdc-button-base + .mat-mdc-button-base {
  margin-left: 8px;
}
[dir=rtl] .mat-mdc-dialog-actions .mat-button-base + .mat-button-base,
[dir=rtl] .mat-mdc-dialog-actions .mat-mdc-button-base + .mat-mdc-button-base {
  margin-left: 0;
  margin-right: 8px;
}

.mat-mdc-dialog-component-host {
  display: contents;
}
`],encapsulation:2})}return n})(),xt="--mat-dialog-transition-duration";function wt(n){return n==null?null:typeof n=="number"?n:n.endsWith("ms")?ce(n.substring(0,n.length-2)):n.endsWith("s")?ce(n.substring(0,n.length-1))*1e3:n==="0"?0:null}var ve=(function(n){return n[n.OPEN=0]="OPEN",n[n.CLOSING=1]="CLOSING",n[n.CLOSED=2]="CLOSED",n})(ve||{}),J=class{_ref;_config;_containerInstance;componentInstance;componentRef=null;disableClose;id;_afterOpened=new Ce(1);_beforeClosed=new Ce(1);_result;_closeFallbackTimeout;_state=ve.OPEN;_closeInteractionType;constructor(s,e,t){this._ref=s,this._config=e,this._containerInstance=t,this.disableClose=e.disableClose,this.id=s.id,s.addPanelClass("mat-mdc-dialog-panel"),t._animationStateChanged.pipe(z(i=>i.state==="opened"),F(1)).subscribe(()=>{this._afterOpened.next(),this._afterOpened.complete()}),t._animationStateChanged.pipe(z(i=>i.state==="closed"),F(1)).subscribe(()=>{clearTimeout(this._closeFallbackTimeout),this._finishDialogClose()}),s.overlayRef.detachments().subscribe(()=>{this._beforeClosed.next(this._result),this._beforeClosed.complete(),this._finishDialogClose()}),ze(this.backdropClick(),this.keydownEvents().pipe(z(i=>i.keyCode===27&&!this.disableClose&&!de(i)))).subscribe(i=>{this.disableClose||(i.preventDefault(),Tt(this,i.type==="keydown"?"keyboard":"mouse"))})}close(s){let e=this._config.closePredicate;e&&!e(s,this._config,this.componentInstance)||(this._result=s,this._containerInstance._animationStateChanged.pipe(z(t=>t.state==="closing"),F(1)).subscribe(t=>{this._beforeClosed.next(s),this._beforeClosed.complete(),this._ref.overlayRef.detachBackdrop(),this._closeFallbackTimeout=setTimeout(()=>this._finishDialogClose(),t.totalTime+100)}),this._state=ve.CLOSING,this._containerInstance._startExitAnimation())}afterOpened(){return this._afterOpened}afterClosed(){return this._ref.closed}beforeClosed(){return this._beforeClosed}backdropClick(){return this._ref.backdropClick}keydownEvents(){return this._ref.keydownEvents}updatePosition(s){let e=this._ref.config.positionStrategy;return s&&(s.left||s.right)?s.left?e.left(s.left):e.right(s.right):e.centerHorizontally(),s&&(s.top||s.bottom)?s.top?e.top(s.top):e.bottom(s.bottom):e.centerVertically(),this._ref.updatePosition(),this}updateSize(s="",e=""){return this._ref.updateSize(s,e),this}addPanelClass(s){return this._ref.addPanelClass(s),this}removePanelClass(s){return this._ref.removePanelClass(s),this}getState(){return this._state}_finishDialogClose(){this._state=ve.CLOSED,this._ref.close(this._result,{focusOrigin:this._closeInteractionType}),this.componentInstance=null}};function Tt(n,s,e){return n._closeInteractionType=s,n.close(e)}var Be=new h("MatMdcDialogData"),Kt=new h("mat-mdc-dialog-default-options"),Yt=new h("mat-mdc-dialog-scroll-strategy",{providedIn:"root",factory:()=>{let n=o(u);return()=>_e(n)}}),Pe=(()=>{class n{_defaultOptions=o(Kt,{optional:!0});_scrollStrategy=o(Yt);_parentDialog=o(n,{optional:!0,skipSelf:!0});_idGenerator=o(k);_injector=o(u);_dialog=o(Oe);_animationsDisabled=L();_openDialogsAtThisLevel=[];_afterAllClosedAtThisLevel=new c;_afterOpenedAtThisLevel=new c;dialogConfigClass=ye;_dialogRefConstructor;_dialogContainerType;_dialogDataToken;get openDialogs(){return this._parentDialog?this._parentDialog.openDialogs:this._openDialogsAtThisLevel}get afterOpened(){return this._parentDialog?this._parentDialog.afterOpened:this._afterOpenedAtThisLevel}_getAfterAllClosed(){let e=this._parentDialog;return e?e._getAfterAllClosed():this._afterAllClosedAtThisLevel}afterAllClosed=te(()=>this.openDialogs.length?this._getAfterAllClosed():this._getAfterAllClosed().pipe(ie(void 0)));constructor(){this._dialogRefConstructor=J,this._dialogContainerType=$t,this._dialogDataToken=Be}open(e,t){let i;t=m(m({},this._defaultOptions||new ye),t),t.id=t.id||this._idGenerator.getId("mat-mdc-dialog-"),t.scrollStrategy=t.scrollStrategy||this._scrollStrategy();let a=this._dialog.open(e,je(m({},t),{positionStrategy:H(this._injector).centerHorizontally().centerVertically(),disableClose:!0,closePredicate:void 0,closeOnDestroy:!1,closeOnOverlayDetachments:!1,disableAnimations:this._animationsDisabled||t.enterAnimationDuration?.toLocaleString()==="0"||t.exitAnimationDuration?.toString()==="0",container:{type:this._dialogContainerType,providers:()=>[{provide:this.dialogConfigClass,useValue:t},{provide:R,useValue:t}]},templateContext:()=>({dialogRef:i}),providers:(r,l,d)=>(i=new this._dialogRefConstructor(r,t,d),i.updatePosition(t?.position),[{provide:this._dialogContainerType,useValue:d},{provide:this._dialogDataToken,useValue:l.data},{provide:this._dialogRefConstructor,useValue:i}])}));return i.componentRef=a.componentRef,i.componentInstance=a.componentInstance,this.openDialogs.push(i),this.afterOpened.next(i),i.afterClosed().subscribe(()=>{let r=this.openDialogs.indexOf(i);r>-1&&(this.openDialogs.splice(r,1),this.openDialogs.length||this._getAfterAllClosed().next())}),i}closeAll(){this._closeDialogs(this.openDialogs)}getDialogById(e){return this.openDialogs.find(t=>t.id===e)}ngOnDestroy(){this._closeDialogs(this._openDialogsAtThisLevel),this._afterAllClosedAtThisLevel.complete(),this._afterOpenedAtThisLevel.complete()}_closeDialogs(e){let t=e.length;for(;t--;)e[t].close()}static \u0275fac=function(t){return new(t||n)};static \u0275prov=x({token:n,factory:n.\u0275fac,providedIn:"root"})}return n})(),Et=(()=>{class n{dialogRef=o(J,{optional:!0});_elementRef=o(y);_dialog=o(Pe);ariaLabel;type="button";dialogResult;_matDialogClose;constructor(){}ngOnInit(){this.dialogRef||(this.dialogRef=Ot(this._elementRef,this._dialog.openDialogs))}ngOnChanges(e){let t=e._matDialogClose||e._matDialogCloseResult;t&&(this.dialogResult=t.currentValue)}_onButtonClick(e){Tt(this.dialogRef,e.screenX===0&&e.screenY===0?"keyboard":"mouse",this.dialogResult)}static \u0275fac=function(t){return new(t||n)};static \u0275dir=p({type:n,selectors:[["","mat-dialog-close",""],["","matDialogClose",""]],hostVars:2,hostBindings:function(t,i){t&1&&E("click",function(r){return i._onButtonClick(r)}),t&2&&A("aria-label",i.ariaLabel||null)("type",i.type)},inputs:{ariaLabel:[0,"aria-label","ariaLabel"],type:"type",dialogResult:[0,"mat-dialog-close","dialogResult"],_matDialogClose:[0,"matDialogClose","_matDialogClose"]},exportAs:["matDialogClose"],features:[ne]})}return n})(),St=(()=>{class n{_dialogRef=o(J,{optional:!0});_elementRef=o(y);_dialog=o(Pe);constructor(){}ngOnInit(){this._dialogRef||(this._dialogRef=Ot(this._elementRef,this._dialog.openDialogs)),this._dialogRef&&Promise.resolve().then(()=>{this._onAdd()})}ngOnDestroy(){this._dialogRef?._containerInstance&&Promise.resolve().then(()=>{this._onRemove()})}static \u0275fac=function(t){return new(t||n)};static \u0275dir=p({type:n})}return n})(),Mt=(()=>{class n extends St{id=o(k).getId("mat-mdc-dialog-title-");_onAdd(){this._dialogRef._containerInstance?._addAriaLabelledBy?.(this.id)}_onRemove(){this._dialogRef?._containerInstance?._removeAriaLabelledBy?.(this.id)}static \u0275fac=(()=>{let e;return function(i){return(e||(e=ae(n)))(i||n)}})();static \u0275dir=p({type:n,selectors:[["","mat-dialog-title",""],["","matDialogTitle",""]],hostAttrs:[1,"mat-mdc-dialog-title","mdc-dialog__title"],hostVars:1,hostBindings:function(t,i){t&2&&Z("id",i.id)},inputs:{id:"id"},exportAs:["matDialogTitle"],features:[T]})}return n})(),It=(()=>{class n{static \u0275fac=function(t){return new(t||n)};static \u0275dir=p({type:n,selectors:[["","mat-dialog-content",""],["mat-dialog-content"],["","matDialogContent",""]],hostAttrs:[1,"mat-mdc-dialog-content","mdc-dialog__content"],features:[$e([bt])]})}return n})(),Rt=(()=>{class n extends St{align;_onAdd(){this._dialogRef._containerInstance?._updateActionSectionCount?.(1)}_onRemove(){this._dialogRef._containerInstance?._updateActionSectionCount?.(-1)}static \u0275fac=(()=>{let e;return function(i){return(e||(e=ae(n)))(i||n)}})();static \u0275dir=p({type:n,selectors:[["","mat-dialog-actions",""],["mat-dialog-actions"],["","matDialogActions",""]],hostAttrs:[1,"mat-mdc-dialog-actions","mdc-dialog__actions"],hostVars:6,hostBindings:function(t,i){t&2&&O("mat-mdc-dialog-actions-align-start",i.align==="start")("mat-mdc-dialog-actions-align-center",i.align==="center")("mat-mdc-dialog-actions-align-end",i.align==="end")},inputs:{align:"align"},features:[T]})}return n})();function Ot(n,s){let e=n.nativeElement.parentElement;for(;e&&!e.classList.contains("mat-mdc-dialog-container");)e=e.parentElement;return e?s.find(t=>t.id===e.id):null}var Ft=(()=>{class n{static \u0275fac=function(t){return new(t||n)};static \u0275mod=D({type:n});static \u0275inj=C({providers:[Pe],imports:[Dt,K,j,he]})}return n})();var Jt=(()=>{class n{static \u0275fac=function(t){return new(t||n)};static \u0275cmp=g({type:n,selectors:[["ng-component"]],hostAttrs:["cdk-text-field-style-loader",""],decls:0,vars:0,template:function(t,i){},styles:[`textarea.cdk-textarea-autosize {
  resize: none;
}

textarea.cdk-textarea-autosize-measuring {
  padding: 2px 0 !important;
  box-sizing: content-box !important;
  height: auto !important;
  overflow: hidden !important;
}

textarea.cdk-textarea-autosize-measuring-firefox {
  padding: 2px 0 !important;
  box-sizing: content-box !important;
  height: 0 !important;
}

@keyframes cdk-text-field-autofill-start { /*!*/ }
@keyframes cdk-text-field-autofill-end { /*!*/ }
.cdk-text-field-autofill-monitored:-webkit-autofill {
  animation: cdk-text-field-autofill-start 0s 1ms;
}

.cdk-text-field-autofill-monitored:not(:-webkit-autofill) {
  animation: cdk-text-field-autofill-end 0s 1ms;
}
`],encapsulation:2,changeDetection:0})}return n})(),ei={passive:!0},Bt=(()=>{class n{_platform=o(M);_ngZone=o(w);_renderer=o(Qe).createRenderer(null,null);_styleLoader=o(nt);_monitoredElements=new Map;constructor(){}monitor(e){if(!this._platform.isBrowser)return He;this._styleLoader.load(Jt);let t=we(e),i=this._monitoredElements.get(t);if(i)return i.subject;let a=new c,r="cdk-text-field-autofilled",l=v=>{v.animationName==="cdk-text-field-autofill-start"&&!t.classList.contains(r)?(t.classList.add(r),this._ngZone.run(()=>a.next({target:v.target,isAutofilled:!0}))):v.animationName==="cdk-text-field-autofill-end"&&t.classList.contains(r)&&(t.classList.remove(r),this._ngZone.run(()=>a.next({target:v.target,isAutofilled:!1})))},d=this._ngZone.runOutsideAngular(()=>(t.classList.add("cdk-text-field-autofill-monitored"),this._renderer.listen(t,"animationstart",l,ei)));return this._monitoredElements.set(t,{subject:a,unlisten:d}),a}stopMonitoring(e){let t=we(e),i=this._monitoredElements.get(t);i&&(i.unlisten(),i.subject.complete(),t.classList.remove("cdk-text-field-autofill-monitored"),t.classList.remove("cdk-text-field-autofilled"),this._monitoredElements.delete(t))}ngOnDestroy(){this._monitoredElements.forEach((e,t)=>this.stopMonitoring(t))}static \u0275fac=function(t){return new(t||n)};static \u0275prov=x({token:n,factory:n.\u0275fac,providedIn:"root"})}return n})();var Pt=(()=>{class n{static \u0275fac=function(t){return new(t||n)};static \u0275mod=D({type:n});static \u0275inj=C({})}return n})();var Lt=new h("MAT_INPUT_VALUE_ACCESSOR");var ti=["button","checkbox","file","hidden","image","radio","range","reset","submit"],ii=new h("MAT_INPUT_CONFIG"),Rn=(()=>{class n{_elementRef=o(y);_platform=o(M);ngControl=o(mt,{optional:!0,self:!0});_autofillMonitor=o(Bt);_ngZone=o(w);_formField=o(gt,{optional:!0});_renderer=o(U);_uid=o(k).getId("mat-input-");_previousNativeValue;_inputValueAccessor;_signalBasedValueAccessor;_previousPlaceholder=null;_errorStateTracker;_config=o(ii,{optional:!0});_cleanupIosKeyup;_cleanupWebkitWheel;_isServer=!1;_isNativeSelect=!1;_isTextarea=!1;_isInFormField=!1;focused=!1;stateChanges=new c;controlType="mat-input";autofilled=!1;get disabled(){return this._disabled}set disabled(e){this._disabled=me(e),this.focused&&(this.focused=!1,this.stateChanges.next())}_disabled=!1;get id(){return this._id}set id(e){this._id=e||this._uid}_id;placeholder;name;get required(){return this._required??this.ngControl?.control?.hasValidator(dt.required)??!1}set required(e){this._required=me(e)}_required;get type(){return this._type}set type(e){this._type=e||"text",this._validateType(),!this._isTextarea&&Te().has(this._type)&&(this._elementRef.nativeElement.type=this._type)}_type="text";get errorStateMatcher(){return this._errorStateTracker.matcher}set errorStateMatcher(e){this._errorStateTracker.matcher=e}userAriaDescribedBy;get value(){return this._signalBasedValueAccessor?this._signalBasedValueAccessor.value():this._inputValueAccessor.value}set value(e){e!==this.value&&(this._signalBasedValueAccessor?this._signalBasedValueAccessor.value.set(e):this._inputValueAccessor.value=e,this.stateChanges.next())}get readonly(){return this._readonly}set readonly(e){this._readonly=me(e)}_readonly=!1;disabledInteractive;get errorState(){return this._errorStateTracker.errorState}set errorState(e){this._errorStateTracker.errorState=e}_neverEmptyInputTypes=["date","datetime","datetime-local","month","time","week"].filter(e=>Te().has(e));constructor(){let e=o(ht,{optional:!0}),t=o(ut,{optional:!0}),i=o(pt),a=o(Lt,{optional:!0,self:!0}),r=this._elementRef.nativeElement,l=r.nodeName.toLowerCase();a?Ke(a.value)?this._signalBasedValueAccessor=a:this._inputValueAccessor=a:this._inputValueAccessor=r,this._previousNativeValue=this.value,this.id=this.id,this._platform.IOS&&this._ngZone.runOutsideAngular(()=>{this._cleanupIosKeyup=this._renderer.listen(r,"keyup",this._iOSKeyupListener)}),this._errorStateTracker=new ft(i,this.ngControl,t,e,this.stateChanges),this._isServer=!this._platform.isBrowser,this._isNativeSelect=l==="select",this._isTextarea=l==="textarea",this._isInFormField=!!this._formField,this.disabledInteractive=this._config?.disabledInteractive||!1,this._isNativeSelect&&(this.controlType=r.multiple?"mat-native-select-multiple":"mat-native-select"),this._signalBasedValueAccessor&&Ze(()=>{this._signalBasedValueAccessor.value(),this.stateChanges.next()})}ngAfterViewInit(){this._platform.isBrowser&&this._autofillMonitor.monitor(this._elementRef.nativeElement).subscribe(e=>{this.autofilled=e.isAutofilled,this.stateChanges.next()})}ngOnChanges(){this.stateChanges.next()}ngOnDestroy(){this.stateChanges.complete(),this._platform.isBrowser&&this._autofillMonitor.stopMonitoring(this._elementRef.nativeElement),this._cleanupIosKeyup?.(),this._cleanupWebkitWheel?.()}ngDoCheck(){this.ngControl&&(this.updateErrorState(),this.ngControl.disabled!==null&&this.ngControl.disabled!==this.disabled&&(this.disabled=this.ngControl.disabled,this.stateChanges.next())),this._dirtyCheckNativeValue(),this._dirtyCheckPlaceholder()}focus(e){this._elementRef.nativeElement.focus(e)}updateErrorState(){this._errorStateTracker.updateErrorState()}_focusChanged(e){if(e!==this.focused){if(!this._isNativeSelect&&e&&this.disabled&&this.disabledInteractive){let t=this._elementRef.nativeElement;t.type==="number"?(t.type="text",t.setSelectionRange(0,0),t.type="number"):t.setSelectionRange(0,0)}this.focused=e,this.stateChanges.next()}}_onInput(){}_dirtyCheckNativeValue(){let e=this._elementRef.nativeElement.value;this._previousNativeValue!==e&&(this._previousNativeValue=e,this.stateChanges.next())}_dirtyCheckPlaceholder(){let e=this._getPlaceholder();if(e!==this._previousPlaceholder){let t=this._elementRef.nativeElement;this._previousPlaceholder=e,e?t.setAttribute("placeholder",e):t.removeAttribute("placeholder")}}_getPlaceholder(){return this.placeholder||null}_validateType(){ti.indexOf(this._type)>-1}_isNeverEmpty(){return this._neverEmptyInputTypes.indexOf(this._type)>-1}_isBadInput(){let e=this._elementRef.nativeElement.validity;return e&&e.badInput}get empty(){return!this._isNeverEmpty()&&!this._elementRef.nativeElement.value&&!this._isBadInput()&&!this.autofilled}get shouldLabelFloat(){if(this._isNativeSelect){let e=this._elementRef.nativeElement,t=e.options[0];return this.focused||e.multiple||!this.empty||!!(e.selectedIndex>-1&&t&&t.label)}else return this.focused&&!this.disabled||!this.empty}get describedByIds(){return this._elementRef.nativeElement.getAttribute("aria-describedby")?.split(" ")||[]}setDescribedByIds(e){let t=this._elementRef.nativeElement;e.length?t.setAttribute("aria-describedby",e.join(" ")):t.removeAttribute("aria-describedby")}onContainerClick(){this.focused||this.focus()}_isInlineSelect(){let e=this._elementRef.nativeElement;return this._isNativeSelect&&(e.multiple||e.size>1)}_iOSKeyupListener=e=>{let t=e.target;!t.value&&t.selectionStart===0&&t.selectionEnd===0&&(t.setSelectionRange(1,1),t.setSelectionRange(0,0))};_getReadonlyAttribute(){return this._isNativeSelect?null:this.readonly||this.disabled&&this.disabledInteractive?"true":null}static \u0275fac=function(t){return new(t||n)};static \u0275dir=p({type:n,selectors:[["input","matInput",""],["textarea","matInput",""],["select","matNativeControl",""],["input","matNativeControl",""],["textarea","matNativeControl",""]],hostAttrs:[1,"mat-mdc-input-element"],hostVars:21,hostBindings:function(t,i){t&1&&E("focus",function(){return i._focusChanged(!0)})("blur",function(){return i._focusChanged(!1)})("input",function(){return i._onInput()}),t&2&&(Z("id",i.id)("disabled",i.disabled&&!i.disabledInteractive)("required",i.required),A("name",i.name||null)("readonly",i._getReadonlyAttribute())("aria-disabled",i.disabled&&i.disabledInteractive?"true":null)("aria-invalid",i.empty&&i.required?null:i.errorState)("aria-required",i.required)("id",i.id),O("mat-input-server",i._isServer)("mat-mdc-form-field-textarea-control",i._isInFormField&&i._isTextarea)("mat-mdc-form-field-input-control",i._isInFormField)("mat-mdc-input-disabled-interactive",i.disabledInteractive)("mdc-text-field__input",i._isInFormField)("mat-mdc-native-select-inline",i._isInlineSelect()))},inputs:{disabled:"disabled",id:"id",placeholder:"placeholder",name:"name",required:"required",type:"type",errorStateMatcher:"errorStateMatcher",userAriaDescribedBy:[0,"aria-describedby","userAriaDescribedBy"],value:"value",readonly:"readonly",disabledInteractive:[2,"disabledInteractive","disabledInteractive",xe]},exportAs:["matInput"],features:[tt([{provide:_t,useExisting:n}]),ne]})}return n})(),On=(()=>{class n{static \u0275fac=function(t){return new(t||n)};static \u0275mod=D({type:n});static \u0275inj=C({imports:[Me,Me,Pt,he]})}return n})();function ni(n,s){if(n&1){let e=et();b(0,"div",1)(1,"button",2),E("click",function(){Ge(e);let i=Ae();return We(i.action())}),S(2),_()()}if(n&2){let e=Ae();f(2),P(" ",e.data.action," ")}}var ai=["label"];function oi(n,s){}var si=Math.pow(2,31)-1,ee=class{_overlayRef;instance;containerInstance;_afterDismissed=new c;_afterOpened=new c;_onAction=new c;_durationTimeoutId;_dismissedByAction=!1;constructor(s,e){this._overlayRef=e,this.containerInstance=s,s._onExit.subscribe(()=>this._finishDismiss())}dismiss(){this._afterDismissed.closed||this.containerInstance.exit(),clearTimeout(this._durationTimeoutId)}dismissWithAction(){this._onAction.closed||(this._dismissedByAction=!0,this._onAction.next(),this._onAction.complete(),this.dismiss()),clearTimeout(this._durationTimeoutId)}closeWithAction(){this.dismissWithAction()}_dismissAfter(s){this._durationTimeoutId=setTimeout(()=>this.dismiss(),Math.min(s,si))}_open(){this._afterOpened.closed||(this._afterOpened.next(),this._afterOpened.complete())}_finishDismiss(){this._overlayRef.dispose(),this._onAction.closed||this._onAction.complete(),this._afterDismissed.next({dismissedByAction:this._dismissedByAction}),this._afterDismissed.complete(),this._dismissedByAction=!1}afterDismissed(){return this._afterDismissed}afterOpened(){return this.containerInstance._onEnter}onAction(){return this._onAction}},Nt=new h("MatSnackBarData"),V=class{politeness="polite";announcementMessage="";viewContainerRef;duration=0;panelClass;direction;data=null;horizontalPosition="center";verticalPosition="bottom"},ri=(()=>{class n{static \u0275fac=function(t){return new(t||n)};static \u0275dir=p({type:n,selectors:[["","matSnackBarLabel",""]],hostAttrs:[1,"mat-mdc-snack-bar-label","mdc-snackbar__label"]})}return n})(),li=(()=>{class n{static \u0275fac=function(t){return new(t||n)};static \u0275dir=p({type:n,selectors:[["","matSnackBarActions",""]],hostAttrs:[1,"mat-mdc-snack-bar-actions","mdc-snackbar__actions"]})}return n})(),ci=(()=>{class n{static \u0275fac=function(t){return new(t||n)};static \u0275dir=p({type:n,selectors:[["","matSnackBarAction",""]],hostAttrs:[1,"mat-mdc-snack-bar-action","mdc-snackbar__action"]})}return n})(),di=(()=>{class n{snackBarRef=o(ee);data=o(Nt);constructor(){}action(){this.snackBarRef.dismissWithAction()}get hasAction(){return!!this.data.action}static \u0275fac=function(t){return new(t||n)};static \u0275cmp=g({type:n,selectors:[["simple-snack-bar"]],hostAttrs:[1,"mat-mdc-simple-snack-bar"],exportAs:["matSnackBar"],decls:3,vars:2,consts:[["matSnackBarLabel",""],["matSnackBarActions",""],["matButton","","matSnackBarAction","",3,"click"]],template:function(t,i){t&1&&(b(0,"div",0),S(1),_(),Ye(2,ni,3,1,"div",1)),t&2&&(f(),P(" ",i.data.message,`
`),f(),Xe(i.hasAction?2:-1))},dependencies:[ue,ri,li,ci],styles:[`.mat-mdc-simple-snack-bar {
  display: flex;
}
.mat-mdc-simple-snack-bar .mat-mdc-snack-bar-label {
  max-height: 50vh;
  overflow: auto;
}
`],encapsulation:2,changeDetection:0})}return n})(),Le="_mat-snack-bar-enter",Ne="_mat-snack-bar-exit",mi=(()=>{class n extends fe{_ngZone=o(w);_elementRef=o(y);_changeDetectorRef=o(re);_platform=o(M);_animationsDisabled=L();snackBarConfig=o(V);_document=o(q);_trackedModals=new Set;_enterFallback;_exitFallback;_injector=o(u);_announceDelay=150;_announceTimeoutId;_destroyed=!1;_portalOutlet;_onAnnounce=new c;_onExit=new c;_onEnter=new c;_animationState="void";_live;_label;_role;_liveElementId=o(k).getId("mat-snack-bar-container-live-");constructor(){super();let e=this.snackBarConfig;e.politeness==="assertive"&&!e.announcementMessage?this._live="assertive":e.politeness==="off"?this._live="off":this._live="polite",this._platform.FIREFOX&&(this._live==="polite"&&(this._role="status"),this._live==="assertive"&&(this._role="alert"))}attachComponentPortal(e){this._assertNotAttached();let t=this._portalOutlet.attachComponentPortal(e);return this._afterPortalAttached(),t}attachTemplatePortal(e){this._assertNotAttached();let t=this._portalOutlet.attachTemplatePortal(e);return this._afterPortalAttached(),t}attachDomPortal=e=>{this._assertNotAttached();let t=this._portalOutlet.attachDomPortal(e);return this._afterPortalAttached(),t};onAnimationEnd(e){e===Ne?this._completeExit():e===Le&&(clearTimeout(this._enterFallback),this._ngZone.run(()=>{this._onEnter.next(),this._onEnter.complete()}))}enter(){this._destroyed||(this._animationState="visible",this._changeDetectorRef.markForCheck(),this._changeDetectorRef.detectChanges(),this._screenReaderAnnounce(),this._animationsDisabled?W(()=>{this._ngZone.run(()=>queueMicrotask(()=>this.onAnimationEnd(Le)))},{injector:this._injector}):(clearTimeout(this._enterFallback),this._enterFallback=setTimeout(()=>{this._elementRef.nativeElement.classList.add("mat-snack-bar-fallback-visible"),this.onAnimationEnd(Le)},200)))}exit(){return this._destroyed?Ve(void 0):(this._ngZone.run(()=>{this._animationState="hidden",this._changeDetectorRef.markForCheck(),this._elementRef.nativeElement.setAttribute("mat-exit",""),clearTimeout(this._announceTimeoutId),this._animationsDisabled?W(()=>{this._ngZone.run(()=>queueMicrotask(()=>this.onAnimationEnd(Ne)))},{injector:this._injector}):(clearTimeout(this._exitFallback),this._exitFallback=setTimeout(()=>this.onAnimationEnd(Ne),200))}),this._onExit)}ngOnDestroy(){this._destroyed=!0,this._clearFromModals(),this._completeExit()}_completeExit(){clearTimeout(this._exitFallback),queueMicrotask(()=>{this._onExit.next(),this._onExit.complete()})}_afterPortalAttached(){let e=this._elementRef.nativeElement,t=this.snackBarConfig.panelClass;t&&(Array.isArray(t)?t.forEach(r=>e.classList.add(r)):e.classList.add(t)),this._exposeToModals();let i=this._label.nativeElement,a="mdc-snackbar__label";i.classList.toggle(a,!i.querySelector(`.${a}`))}_exposeToModals(){let e=this._liveElementId,t=this._document.querySelectorAll('body > .cdk-overlay-container [aria-modal="true"]');for(let i=0;i<t.length;i++){let a=t[i],r=a.getAttribute("aria-owns");this._trackedModals.add(a),r?r.indexOf(e)===-1&&a.setAttribute("aria-owns",r+" "+e):a.setAttribute("aria-owns",e)}}_clearFromModals(){this._trackedModals.forEach(e=>{let t=e.getAttribute("aria-owns");if(t){let i=t.replace(this._liveElementId,"").trim();i.length>0?e.setAttribute("aria-owns",i):e.removeAttribute("aria-owns")}}),this._trackedModals.clear()}_assertNotAttached(){this._portalOutlet.hasAttached()}_screenReaderAnnounce(){this._announceTimeoutId||this._ngZone.runOutsideAngular(()=>{this._announceTimeoutId=setTimeout(()=>{if(this._destroyed)return;let e=this._elementRef.nativeElement,t=e.querySelector("[aria-hidden]"),i=e.querySelector("[aria-live]");if(t&&i){let a=null;this._platform.isBrowser&&document.activeElement instanceof HTMLElement&&t.contains(document.activeElement)&&(a=document.activeElement),t.removeAttribute("aria-hidden"),i.appendChild(t),a?.focus(),this._onAnnounce.next(),this._onAnnounce.complete()}},this._announceDelay)})}static \u0275fac=function(t){return new(t||n)};static \u0275cmp=g({type:n,selectors:[["mat-snack-bar-container"]],viewQuery:function(t,i){if(t&1&&se(I,7)(ai,7),t&2){let a;Q(a=$())&&(i._portalOutlet=a.first),Q(a=$())&&(i._label=a.first)}},hostAttrs:[1,"mdc-snackbar","mat-mdc-snack-bar-container"],hostVars:6,hostBindings:function(t,i){t&1&&E("animationend",function(r){return i.onAnimationEnd(r.animationName)})("animationcancel",function(r){return i.onAnimationEnd(r.animationName)}),t&2&&O("mat-snack-bar-container-enter",i._animationState==="visible")("mat-snack-bar-container-exit",i._animationState==="hidden")("mat-snack-bar-container-animations-enabled",!i._animationsDisabled)},features:[T],decls:6,vars:3,consts:[["label",""],[1,"mdc-snackbar__surface","mat-mdc-snackbar-surface"],[1,"mat-mdc-snack-bar-label"],["aria-hidden","true"],["cdkPortalOutlet",""]],template:function(t,i){t&1&&(b(0,"div",1)(1,"div",2,0)(3,"div",3),B(4,oi,0,0,"ng-template",4),_(),Je(5,"div"),_()()),t&2&&(f(5),A("aria-live",i._live)("role",i._role)("id",i._liveElementId))},dependencies:[I],styles:[`@keyframes _mat-snack-bar-enter {
  from {
    transform: scale(0.8);
    opacity: 0;
  }
  to {
    transform: scale(1);
    opacity: 1;
  }
}
@keyframes _mat-snack-bar-exit {
  from {
    opacity: 1;
  }
  to {
    opacity: 0;
  }
}
.mat-mdc-snack-bar-container {
  display: flex;
  align-items: center;
  justify-content: center;
  box-sizing: border-box;
  -webkit-tap-highlight-color: rgba(0, 0, 0, 0);
  margin: 8px;
}
.mat-mdc-snack-bar-handset .mat-mdc-snack-bar-container {
  width: 100vw;
}

.mat-snack-bar-container-animations-enabled {
  opacity: 0;
}
.mat-snack-bar-container-animations-enabled.mat-snack-bar-fallback-visible {
  opacity: 1;
}
.mat-snack-bar-container-animations-enabled.mat-snack-bar-container-enter {
  animation: _mat-snack-bar-enter 150ms cubic-bezier(0, 0, 0.2, 1) forwards;
}
.mat-snack-bar-container-animations-enabled.mat-snack-bar-container-exit {
  animation: _mat-snack-bar-exit 75ms cubic-bezier(0.4, 0, 1, 1) forwards;
}

.mat-mdc-snackbar-surface {
  box-shadow: 0px 3px 5px -1px rgba(0, 0, 0, 0.2), 0px 6px 10px 0px rgba(0, 0, 0, 0.14), 0px 1px 18px 0px rgba(0, 0, 0, 0.12);
  display: flex;
  align-items: center;
  justify-content: flex-start;
  box-sizing: border-box;
  padding-left: 0;
  padding-right: 8px;
}
[dir=rtl] .mat-mdc-snackbar-surface {
  padding-right: 0;
  padding-left: 8px;
}
.mat-mdc-snack-bar-container .mat-mdc-snackbar-surface {
  min-width: 344px;
  max-width: 672px;
}
.mat-mdc-snack-bar-handset .mat-mdc-snackbar-surface {
  width: 100%;
  min-width: 0;
}
@media (forced-colors: active) {
  .mat-mdc-snackbar-surface {
    outline: solid 1px;
  }
}
.mat-mdc-snack-bar-container .mat-mdc-snackbar-surface {
  color: var(--mat-snack-bar-supporting-text-color, var(--mat-sys-inverse-on-surface));
  border-radius: var(--mat-snack-bar-container-shape, var(--mat-sys-corner-extra-small));
  background-color: var(--mat-snack-bar-container-color, var(--mat-sys-inverse-surface));
}

.mdc-snackbar__label {
  width: 100%;
  flex-grow: 1;
  box-sizing: border-box;
  margin: 0;
  padding: 14px 8px 14px 16px;
}
[dir=rtl] .mdc-snackbar__label {
  padding-left: 8px;
  padding-right: 16px;
}
.mat-mdc-snack-bar-container .mdc-snackbar__label {
  font-family: var(--mat-snack-bar-supporting-text-font, var(--mat-sys-body-medium-font));
  font-size: var(--mat-snack-bar-supporting-text-size, var(--mat-sys-body-medium-size));
  font-weight: var(--mat-snack-bar-supporting-text-weight, var(--mat-sys-body-medium-weight));
  line-height: var(--mat-snack-bar-supporting-text-line-height, var(--mat-sys-body-medium-line-height));
}

.mat-mdc-snack-bar-actions {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  box-sizing: border-box;
}

.mat-mdc-snack-bar-handset,
.mat-mdc-snack-bar-container,
.mat-mdc-snack-bar-label {
  flex: 1 1 auto;
}

.mat-mdc-snack-bar-container .mat-mdc-button.mat-mdc-snack-bar-action:not(:disabled).mat-unthemed {
  color: var(--mat-snack-bar-button-color, var(--mat-sys-inverse-primary));
}
.mat-mdc-snack-bar-container .mat-mdc-button.mat-mdc-snack-bar-action:not(:disabled) {
  --mat-button-text-state-layer-color: currentColor;
  --mat-button-text-ripple-color: currentColor;
}
.mat-mdc-snack-bar-container .mat-mdc-button.mat-mdc-snack-bar-action:not(:disabled) .mat-ripple-element {
  opacity: 0.1;
}
`],encapsulation:2})}return n})(),hi=new h("mat-snack-bar-default-options",{providedIn:"root",factory:()=>new V}),Xn=(()=>{class n{_live=o(rt);_injector=o(u);_breakpointObserver=o(at);_parentSnackBar=o(n,{optional:!0,skipSelf:!0});_defaultConfig=o(hi);_animationsDisabled=L();_snackBarRefAtThisLevel=null;simpleSnackBarComponent=di;snackBarContainerComponent=mi;handsetCssClass="mat-mdc-snack-bar-handset";get _openedSnackBarRef(){let e=this._parentSnackBar;return e?e._openedSnackBarRef:this._snackBarRefAtThisLevel}set _openedSnackBarRef(e){this._parentSnackBar?this._parentSnackBar._openedSnackBarRef=e:this._snackBarRefAtThisLevel=e}constructor(){}openFromComponent(e,t){return this._attach(e,t)}openFromTemplate(e,t){return this._attach(e,t)}open(e,t="",i){let a=m(m({},this._defaultConfig),i);return a.data={message:e,action:t},a.announcementMessage===e&&(a.announcementMessage=void 0),this.openFromComponent(this.simpleSnackBarComponent,a)}dismiss(){this._openedSnackBarRef&&this._openedSnackBarRef.dismiss()}ngOnDestroy(){this._snackBarRefAtThisLevel&&this._snackBarRefAtThisLevel.dismiss()}_attachSnackBarContainer(e,t){let i=t&&t.viewContainerRef&&t.viewContainerRef.injector,a=u.create({parent:i||this._injector,providers:[{provide:V,useValue:t}]}),r=new N(this.snackBarContainerComponent,t.viewContainerRef,a),l=e.attach(r);return l.instance.snackBarConfig=t,l.instance}_attach(e,t){let i=m(m(m({},new V),this._defaultConfig),t),a=this._createOverlay(i),r=this._attachSnackBarContainer(a,i),l=new ee(r,a);if(e instanceof oe){let d=new pe(e,null,{$implicit:i.data,snackBarRef:l});l.instance=r.attachTemplatePortal(d)}else{let d=this._createInjector(i,l),v=new N(e,void 0,d),Ht=r.attachComponentPortal(v);l.instance=Ht.instance}return this._breakpointObserver.observe(ct.HandsetPortrait).pipe(qe(a.detachments())).subscribe(d=>{a.overlayElement.classList.toggle(this.handsetCssClass,d.matches)}),i.announcementMessage&&r._onAnnounce.subscribe(()=>{this._live.announce(i.announcementMessage,i.politeness)}),this._animateSnackBar(l,i),this._openedSnackBarRef=l,this._openedSnackBarRef}_animateSnackBar(e,t){e.afterDismissed().subscribe(()=>{this._openedSnackBarRef==e&&(this._openedSnackBarRef=null),t.announcementMessage&&this._live.clear()}),t.duration&&t.duration>0&&e.afterOpened().subscribe(()=>e._dismissAfter(t.duration)),this._openedSnackBarRef?(this._openedSnackBarRef.afterDismissed().subscribe(()=>{e.containerInstance.enter()}),this._openedSnackBarRef.dismiss()):e.containerInstance.enter()}_createOverlay(e){let t=new ge;t.direction=e.direction;let i=H(this._injector),a=e.direction==="rtl",r=e.horizontalPosition==="left"||e.horizontalPosition==="start"&&!a||e.horizontalPosition==="end"&&a,l=!r&&e.horizontalPosition!=="center";return r?i.left("0"):l?i.right("0"):i.centerHorizontally(),e.verticalPosition==="top"?i.top("0"):i.bottom("0"),t.positionStrategy=i,t.disableAnimations=this._animationsDisabled,be(this._injector,t)}_createInjector(e,t){let i=e&&e.viewContainerRef&&e.viewContainerRef.injector;return u.create({parent:i||this._injector,providers:[{provide:ee,useValue:t},{provide:Nt,useValue:e.data}]})}static \u0275fac=function(t){return new(t||n)};static \u0275prov=x({token:n,factory:n.\u0275fac,providedIn:"root"})}return n})();var jt=class n{data=o(Be);static \u0275fac=function(e){return new(e||n)};static \u0275cmp=g({type:n,selectors:[["app-confirm-dialog"]],decls:10,vars:6,consts:[["mat-dialog-title",""],["align","end"],["mat-button","",3,"mat-dialog-close"],["mat-flat-button","","color","warn",3,"mat-dialog-close"]],template:function(e,t){e&1&&(b(0,"h2",0),S(1),_(),b(2,"mat-dialog-content")(3,"p"),S(4),_()(),b(5,"mat-dialog-actions",1)(6,"button",2),S(7),_(),b(8,"button",3),S(9),_()()),e&2&&(f(),ke(t.data.title),f(3),ke(t.data.message),f(2),De("mat-dialog-close",!1),f(),P(" ",t.data.cancelLabel," "),f(),De("mat-dialog-close",!0),f(),P(" ",t.data.confirmLabel," "))},dependencies:[Se,ue,Ft,Et,Mt,Rt,It],encapsulation:2})};export{Pe as a,Rn as b,On as c,Xn as d,jt as e};
/**i18n:50891650dad0b0d4917b39accc1751b6ed5530f2b9ce6751d1c125c5daff0d87*/
