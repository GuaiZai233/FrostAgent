import{a as an}from"./chunk-HGSQD6WB.js";import{a as Yt,b as Jt,c as en,d as tn,e as nn}from"./chunk-YJ27GO6D.js";import{A as Qt,B as Xt,C as jt,D as qt,E as Kt,F as Zt,G as Ut,a as Lt,c as Ot,d as Gt,g as Ft,i as $t,j as Se,w as zt,x as Vt,y as Ht,z as Wt}from"./chunk-RZQOW22N.js";import{a as At}from"./chunk-IPQKSYT4.js";import{$ as x,$a as r,$c as Ct,A as qe,Aa as fe,Ab as O,Ca as nt,Cb as V,E as Ke,Fc as K,Ga as N,Gb as xe,Ha as me,Ia as $,J as Ze,Jc as ne,K as oe,Ka as Y,L as Ue,La as D,Lc as ue,M as U,Ma as at,Mc as mt,Nb as j,Nc as Te,Oc as pe,Pb as h,Qb as q,Qc as bt,R as Ye,Rc as _t,Sc as ht,T as re,Ta as it,Tc as ut,Ua as M,Uc as pt,V as B,Va as w,Vc as gt,Wa as R,Wc as ft,X as s,Xa as ke,Xc as kt,Ya as ve,Yc as vt,Za as ye,Zc as yt,_a as _,_c as xt,_d as Bt,aa as C,ab as c,ba as Je,bb as S,ca as et,cd as Tt,d as G,da as ge,eb as J,ec as Ce,ed as St,f as We,fb as ee,gd as Et,h as ae,ha as T,hb as A,ia as F,ib as ot,ic as ct,jb as f,jc as _e,jd as It,kb as b,kd as Mt,la as v,lb as m,m as Qe,mb as W,na as ce,nb as Q,nc as st,nd as Pt,o as Xe,oa as se,ob as be,od as wt,pa as L,pb as z,pd as Rt,qa as tt,qb as u,qd as Nt,rb as p,rd as Dt,tc as dt,va as l,vb as te,vc as lt,wb as rt,wc as he,xa as de,xb as E,y as je,ya as le,yb as X,z as ie,zb as k}from"./chunk-SZWSLCWU.js";var pn=["input"],gn=["label"],fn=["*"],Ee={color:"accent",clickAction:"check-indeterminate",disabledInteractive:!1},kn=new B("mat-checkbox-default-options",{providedIn:"root",factory:()=>Ee}),y=function(a){return a[a.Init=0]="Init",a[a.Checked=1]="Checked",a[a.Unchecked=2]="Unchecked",a[a.Indeterminate=3]="Indeterminate",a;}(y||{}),Ie=class{source;checked;},Me=(()=>{class a{_elementRef=s(L);_changeDetectorRef=s(j);_ngZone=s(F);_animationsDisabled=K();_options=s(kn,{optional:!0});focus(){this._inputElement.nativeElement.focus();}_createChangeEvent(e){let t=new Ie();return t.source=this,t.checked=e,t;}_getAnimationTargetElement(){return this._inputElement?.nativeElement;}_animationClasses={uncheckedToChecked:"mdc-checkbox--anim-unchecked-checked",uncheckedToIndeterminate:"mdc-checkbox--anim-unchecked-indeterminate",checkedToUnchecked:"mdc-checkbox--anim-checked-unchecked",checkedToIndeterminate:"mdc-checkbox--anim-checked-indeterminate",indeterminateToChecked:"mdc-checkbox--anim-indeterminate-checked",indeterminateToUnchecked:"mdc-checkbox--anim-indeterminate-unchecked"};ariaLabel="";ariaLabelledby=null;ariaDescribedby;ariaExpanded;ariaControls;ariaOwns;_uniqueId;id;get inputId(){return`${this.id||this._uniqueId}-input`;}required=!1;labelPosition="after";name=null;change=new T();indeterminateChange=new T();value;disableRipple=!1;_inputElement;_labelElement;tabIndex;color;disabledInteractive;_onTouched=()=>{};_currentAnimationClass="";_currentCheckState=y.Init;_controlValueAccessorChangeFn=()=>{};_validatorChangeFn=()=>{};constructor(){s(_e).load(ue);let e=s(new xe("tabindex"),{optional:!0});this._options=this._options||Ee,this.color=this._options.color||Ee.color,this.tabIndex=e==null?0:parseInt(e)||0,this.id=this._uniqueId=s(he).getId("mat-mdc-checkbox-"),this.disabledInteractive=this._options?.disabledInteractive??!1;}ngOnChanges(e){e.required&&this._validatorChangeFn();}ngAfterViewInit(){this._syncIndeterminate(this.indeterminate);}get checked(){return this._checked;}set checked(e){e!=this.checked&&(this._checked=e,this._changeDetectorRef.markForCheck());}_checked=!1;get disabled(){return this._disabled;}set disabled(e){e!==this.disabled&&(this._disabled=e,this._changeDetectorRef.markForCheck());}_disabled=!1;get indeterminate(){return this._indeterminate();}set indeterminate(e){let t=e!=this._indeterminate();this._indeterminate.set(e),t&&(e?this._transitionCheckState(y.Indeterminate):this._transitionCheckState(this.checked?y.Checked:y.Unchecked),this.indeterminateChange.emit(e)),this._syncIndeterminate(e);}_indeterminate=v(!1);_isRippleDisabled(){return this.disableRipple||this.disabled;}_onLabelTextChange(){this._changeDetectorRef.detectChanges();}writeValue(e){this.checked=!!e;}registerOnChange(e){this._controlValueAccessorChangeFn=e;}registerOnTouched(e){this._onTouched=e;}setDisabledState(e){this.disabled=e;}validate(e){return this.required&&e.value!==!0?{required:!0}:null;}registerOnValidatorChange(e){this._validatorChangeFn=e;}_transitionCheckState(e){let t=this._currentCheckState,n=this._getAnimationTargetElement();if(!(t===e||!n)&&(this._currentAnimationClass&&n.classList.remove(this._currentAnimationClass),this._currentAnimationClass=this._getAnimationClassForCheckStateTransition(t,e),this._currentCheckState=e,this._currentAnimationClass.length>0)){n.classList.add(this._currentAnimationClass);let i=this._currentAnimationClass;this._ngZone.runOutsideAngular(()=>{setTimeout(()=>{n.classList.remove(i);},1e3);});}}_emitChangeEvent(){this._controlValueAccessorChangeFn(this.checked),this.change.emit(this._createChangeEvent(this.checked)),this._inputElement&&(this._inputElement.nativeElement.checked=this.checked);}toggle(){this.checked=!this.checked,this._controlValueAccessorChangeFn(this.checked);}_handleInputClick(){let e=this._options?.clickAction;!this.disabled&&e!=="noop"?(this.indeterminate&&e!=="check"&&Promise.resolve().then(()=>{this._indeterminate.set(!1),this.indeterminateChange.emit(!1);}),this._checked=!this._checked,this._transitionCheckState(this._checked?y.Checked:y.Unchecked),this._emitChangeEvent()):(this.disabled&&this.disabledInteractive||!this.disabled&&e==="noop")&&(this._inputElement.nativeElement.checked=this.checked,this._inputElement.nativeElement.indeterminate=this.indeterminate);}_onInteractionEvent(e){e.stopPropagation();}_onBlur(){Promise.resolve().then(()=>{this._onTouched(),this._changeDetectorRef.markForCheck();});}_getAnimationClassForCheckStateTransition(e,t){if(this._animationsDisabled)return"";switch(e){case y.Init:if(t===y.Checked)return this._animationClasses.uncheckedToChecked;if(t==y.Indeterminate)return this._checked?this._animationClasses.checkedToIndeterminate:this._animationClasses.uncheckedToIndeterminate;break;case y.Unchecked:return t===y.Checked?this._animationClasses.uncheckedToChecked:this._animationClasses.uncheckedToIndeterminate;case y.Checked:return t===y.Unchecked?this._animationClasses.checkedToUnchecked:this._animationClasses.checkedToIndeterminate;case y.Indeterminate:return t===y.Checked?this._animationClasses.indeterminateToChecked:this._animationClasses.indeterminateToUnchecked;}return"";}_syncIndeterminate(e){let t=this._inputElement;t&&(t.nativeElement.indeterminate=e);}_onInputClick(){this._handleInputClick();}_onTouchTargetClick(){this._handleInputClick(),this.disabled||this._inputElement.nativeElement.focus();}_preventBubblingFromLabel(e){e.target&&this._labelElement.nativeElement.contains(e.target)&&e.stopPropagation();}static ɵfac=function(t){return new(t||a)();};static ɵcmp=N({type:a,selectors:[["mat-checkbox"]],viewQuery:function(t,n){if(t&1&&z(pn,5)(gn,5),t&2){let i;u(i=p())&&(n._inputElement=i.first),u(i=p())&&(n._labelElement=i.first);}},hostAttrs:[1,"mat-mdc-checkbox"],hostVars:16,hostBindings:function(t,n){t&2&&(ot("id",n.id),M("tabindex",null)("aria-label",null)("aria-labelledby",null),X(n.color?"mat-"+n.color:"mat-accent"),E("_mat-animation-noopable",n._animationsDisabled)("mdc-checkbox--disabled",n.disabled)("mat-mdc-checkbox-disabled",n.disabled)("mat-mdc-checkbox-checked",n.checked)("mat-mdc-checkbox-disabled-interactive",n.disabledInteractive));},inputs:{ariaLabel:[0,"aria-label","ariaLabel"],ariaLabelledby:[0,"aria-labelledby","ariaLabelledby"],ariaDescribedby:[0,"aria-describedby","ariaDescribedby"],ariaExpanded:[2,"aria-expanded","ariaExpanded",h],ariaControls:[0,"aria-controls","ariaControls"],ariaOwns:[0,"aria-owns","ariaOwns"],id:"id",required:[2,"required","required",h],labelPosition:"labelPosition",name:"name",value:"value",disableRipple:[2,"disableRipple","disableRipple",h],tabIndex:[2,"tabIndex","tabIndex",e=>e==null?void 0:q(e)],color:"color",disabledInteractive:[2,"disabledInteractive","disabledInteractive",h],checked:[2,"checked","checked",h],disabled:[2,"disabled","disabled",h],indeterminate:[2,"indeterminate","indeterminate",h]},outputs:{change:"change",indeterminateChange:"indeterminateChange"},exportAs:["matCheckbox"],features:[V([{provide:yt,useExisting:Ye(()=>a),multi:!0},{provide:Ct,useExisting:a,multi:!0}]),ce],ngContentSelectors:fn,decls:15,vars:23,consts:[["checkbox",""],["input",""],["label",""],["mat-internal-form-field","",3,"click","labelPosition"],[1,"mdc-checkbox"],["aria-hidden","true",1,"mat-mdc-checkbox-touch-target",3,"click"],["type","checkbox",1,"mdc-checkbox__native-control",3,"blur","click","change","checked","indeterminate","disabled","id","required","tabIndex"],["aria-hidden","true",1,"mdc-checkbox__ripple"],["aria-hidden","true",1,"mdc-checkbox__background"],["focusable","false","viewBox","0 0 24 24",1,"mdc-checkbox__checkmark"],["fill","none","d","M1.73,12.91 8.1,19.28 22.79,4.59",1,"mdc-checkbox__checkmark-path"],[1,"mdc-checkbox__mixedmark"],["mat-ripple","","aria-hidden","true",1,"mat-mdc-checkbox-ripple","mat-focus-indicator",3,"matRippleTrigger","matRippleDisabled","matRippleCentered"],[1,"mdc-label",3,"for"]],template:function(t,n){if(t&1&&(W(),r(0,"div",3),b("click",function(d){return n._preventBubblingFromLabel(d);}),r(1,"div",4,0)(3,"div",5),b("click",function(){return n._onTouchTargetClick();}),c(),r(4,"input",6,1),b("blur",function(){return n._onBlur();})("click",function(){return n._onInputClick();})("change",function(d){return n._onInteractionEvent(d);}),c(),S(6,"div",7),r(7,"div",8),Je(),r(8,"svg",9),S(9,"path",10),c(),et(),S(10,"div",11),c(),S(11,"div",12),c(),r(12,"label",13,2),Q(14),c()()),t&2){let i=te(2);_("labelPosition",n.labelPosition),l(4),E("mdc-checkbox--selected",n.checked),_("checked",n.checked)("indeterminate",n.indeterminate)("disabled",n.disabled&&!n.disabledInteractive)("id",n.inputId)("required",n.required)("tabIndex",n.disabled&&!n.disabledInteractive?-1:n.tabIndex),M("aria-label",n.ariaLabel||null)("aria-labelledby",n.ariaLabelledby)("aria-describedby",n.ariaDescribedby)("aria-checked",n.indeterminate?"mixed":null)("aria-controls",n.ariaControls)("aria-disabled",n.disabled&&n.disabledInteractive?!0:null)("aria-expanded",n.ariaExpanded)("aria-owns",n.ariaOwns)("name",n.name)("value",n.value),l(7),_("matRippleTrigger",i)("matRippleDisabled",n.disableRipple||n.disabled)("matRippleCentered",!0),l(),_("for",n.inputId);}},dependencies:[ne,an],styles:[`.mdc-checkbox {
  display: inline-block;
  position: relative;
  flex: 0 0 18px;
  box-sizing: content-box;
  width: 18px;
  height: 18px;
  line-height: 0;
  white-space: nowrap;
  cursor: pointer;
  vertical-align: bottom;
  padding: calc((var(--mat-checkbox-state-layer-size, 40px) - 18px) / 2);
  margin: calc((var(--mat-checkbox-state-layer-size, 40px) - var(--mat-checkbox-state-layer-size, 40px)) / 2);
}
.mdc-checkbox:hover > .mdc-checkbox__ripple {
  opacity: var(--mat-checkbox-unselected-hover-state-layer-opacity, var(--mat-sys-hover-state-layer-opacity));
  background-color: var(--mat-checkbox-unselected-hover-state-layer-color, var(--mat-sys-on-surface));
}
.mdc-checkbox:hover > .mat-mdc-checkbox-ripple > .mat-ripple-element {
  background-color: var(--mat-checkbox-unselected-hover-state-layer-color, var(--mat-sys-on-surface));
}
.mdc-checkbox .mdc-checkbox__native-control:focus + .mdc-checkbox__ripple {
  opacity: var(--mat-checkbox-unselected-focus-state-layer-opacity, var(--mat-sys-focus-state-layer-opacity));
  background-color: var(--mat-checkbox-unselected-focus-state-layer-color, var(--mat-sys-on-surface));
}
.mdc-checkbox .mdc-checkbox__native-control:focus ~ .mat-mdc-checkbox-ripple .mat-ripple-element {
  background-color: var(--mat-checkbox-unselected-focus-state-layer-color, var(--mat-sys-on-surface));
}
.mdc-checkbox:active > .mdc-checkbox__native-control + .mdc-checkbox__ripple {
  opacity: var(--mat-checkbox-unselected-pressed-state-layer-opacity, var(--mat-sys-pressed-state-layer-opacity));
  background-color: var(--mat-checkbox-unselected-pressed-state-layer-color, var(--mat-sys-primary));
}
.mdc-checkbox:active > .mdc-checkbox__native-control ~ .mat-mdc-checkbox-ripple .mat-ripple-element {
  background-color: var(--mat-checkbox-unselected-pressed-state-layer-color, var(--mat-sys-primary));
}
.mdc-checkbox:hover > .mdc-checkbox__native-control:checked + .mdc-checkbox__ripple {
  opacity: var(--mat-checkbox-selected-hover-state-layer-opacity, var(--mat-sys-hover-state-layer-opacity));
  background-color: var(--mat-checkbox-selected-hover-state-layer-color, var(--mat-sys-primary));
}
.mdc-checkbox:hover > .mdc-checkbox__native-control:checked ~ .mat-mdc-checkbox-ripple .mat-ripple-element {
  background-color: var(--mat-checkbox-selected-hover-state-layer-color, var(--mat-sys-primary));
}
.mdc-checkbox .mdc-checkbox__native-control:focus:checked + .mdc-checkbox__ripple {
  opacity: var(--mat-checkbox-selected-focus-state-layer-opacity, var(--mat-sys-focus-state-layer-opacity));
  background-color: var(--mat-checkbox-selected-focus-state-layer-color, var(--mat-sys-primary));
}
.mdc-checkbox .mdc-checkbox__native-control:focus:checked ~ .mat-mdc-checkbox-ripple .mat-ripple-element {
  background-color: var(--mat-checkbox-selected-focus-state-layer-color, var(--mat-sys-primary));
}
.mdc-checkbox:active > .mdc-checkbox__native-control:checked + .mdc-checkbox__ripple {
  opacity: var(--mat-checkbox-selected-pressed-state-layer-opacity, var(--mat-sys-pressed-state-layer-opacity));
  background-color: var(--mat-checkbox-selected-pressed-state-layer-color, var(--mat-sys-on-surface));
}
.mdc-checkbox:active > .mdc-checkbox__native-control:checked ~ .mat-mdc-checkbox-ripple .mat-ripple-element {
  background-color: var(--mat-checkbox-selected-pressed-state-layer-color, var(--mat-sys-on-surface));
}
.mdc-checkbox--disabled.mat-mdc-checkbox-disabled-interactive .mdc-checkbox .mdc-checkbox__native-control ~ .mat-mdc-checkbox-ripple .mat-ripple-element,
.mdc-checkbox--disabled.mat-mdc-checkbox-disabled-interactive .mdc-checkbox .mdc-checkbox__native-control + .mdc-checkbox__ripple {
  background-color: var(--mat-checkbox-unselected-hover-state-layer-color, var(--mat-sys-on-surface));
}
.mdc-checkbox .mdc-checkbox__native-control {
  position: absolute;
  margin: 0;
  padding: 0;
  opacity: 0;
  cursor: inherit;
  z-index: 1;
  width: var(--mat-checkbox-state-layer-size, 40px);
  height: var(--mat-checkbox-state-layer-size, 40px);
  top: calc((var(--mat-checkbox-state-layer-size, 40px) - var(--mat-checkbox-state-layer-size, 40px)) / 2);
  right: calc((var(--mat-checkbox-state-layer-size, 40px) - var(--mat-checkbox-state-layer-size, 40px)) / 2);
  left: calc((var(--mat-checkbox-state-layer-size, 40px) - var(--mat-checkbox-state-layer-size, 40px)) / 2);
}

.mdc-checkbox--disabled {
  cursor: default;
  pointer-events: none;
}

.mdc-checkbox__background {
  display: inline-flex;
  position: absolute;
  align-items: center;
  justify-content: center;
  box-sizing: border-box;
  width: 18px;
  height: 18px;
  border: 2px solid currentColor;
  border-radius: 2px;
  background-color: transparent;
  pointer-events: none;
  will-change: background-color, border-color;
  transition: background-color 90ms cubic-bezier(0.4, 0, 0.6, 1), border-color 90ms cubic-bezier(0.4, 0, 0.6, 1);
  -webkit-print-color-adjust: exact;
  color-adjust: exact;
  border-color: var(--mat-checkbox-unselected-icon-color, var(--mat-sys-on-surface-variant));
  top: calc((var(--mat-checkbox-state-layer-size, 40px) - 18px) / 2);
  left: calc((var(--mat-checkbox-state-layer-size, 40px) - 18px) / 2);
}

.mdc-checkbox__native-control:enabled:checked ~ .mdc-checkbox__background,
.mdc-checkbox__native-control:enabled:indeterminate ~ .mdc-checkbox__background {
  border-color: var(--mat-checkbox-selected-icon-color, var(--mat-sys-primary));
  background-color: var(--mat-checkbox-selected-icon-color, var(--mat-sys-primary));
}

.mdc-checkbox--disabled .mdc-checkbox__background {
  border-color: var(--mat-checkbox-disabled-unselected-icon-color, color-mix(in srgb, var(--mat-sys-on-surface) 38%, transparent));
}
@media (forced-colors: active) {
  .mdc-checkbox--disabled .mdc-checkbox__background {
    border-color: GrayText;
  }
}

.mdc-checkbox__native-control:disabled:checked ~ .mdc-checkbox__background,
.mdc-checkbox__native-control:disabled:indeterminate ~ .mdc-checkbox__background {
  background-color: var(--mat-checkbox-disabled-selected-icon-color, color-mix(in srgb, var(--mat-sys-on-surface) 38%, transparent));
  border-color: transparent;
}
@media (forced-colors: active) {
  .mdc-checkbox__native-control:disabled:checked ~ .mdc-checkbox__background,
  .mdc-checkbox__native-control:disabled:indeterminate ~ .mdc-checkbox__background {
    border-color: GrayText;
  }
}

.mdc-checkbox:hover > .mdc-checkbox__native-control:not(:checked) ~ .mdc-checkbox__background,
.mdc-checkbox:hover > .mdc-checkbox__native-control:not(:indeterminate) ~ .mdc-checkbox__background {
  border-color: var(--mat-checkbox-unselected-hover-icon-color, var(--mat-sys-on-surface));
  background-color: transparent;
}

.mdc-checkbox:hover > .mdc-checkbox__native-control:checked ~ .mdc-checkbox__background,
.mdc-checkbox:hover > .mdc-checkbox__native-control:indeterminate ~ .mdc-checkbox__background {
  border-color: var(--mat-checkbox-selected-hover-icon-color, var(--mat-sys-primary));
  background-color: var(--mat-checkbox-selected-hover-icon-color, var(--mat-sys-primary));
}

.mdc-checkbox__native-control:focus:focus:not(:checked) ~ .mdc-checkbox__background,
.mdc-checkbox__native-control:focus:focus:not(:indeterminate) ~ .mdc-checkbox__background {
  border-color: var(--mat-checkbox-unselected-focus-icon-color, var(--mat-sys-on-surface));
}

.mdc-checkbox__native-control:focus:focus:checked ~ .mdc-checkbox__background,
.mdc-checkbox__native-control:focus:focus:indeterminate ~ .mdc-checkbox__background {
  border-color: var(--mat-checkbox-selected-focus-icon-color, var(--mat-sys-primary));
  background-color: var(--mat-checkbox-selected-focus-icon-color, var(--mat-sys-primary));
}

.mdc-checkbox--disabled.mat-mdc-checkbox-disabled-interactive .mdc-checkbox:hover > .mdc-checkbox__native-control ~ .mdc-checkbox__background,
.mdc-checkbox--disabled.mat-mdc-checkbox-disabled-interactive .mdc-checkbox .mdc-checkbox__native-control:focus ~ .mdc-checkbox__background,
.mdc-checkbox--disabled.mat-mdc-checkbox-disabled-interactive .mdc-checkbox__background {
  border-color: var(--mat-checkbox-disabled-unselected-icon-color, color-mix(in srgb, var(--mat-sys-on-surface) 38%, transparent));
}
@media (forced-colors: active) {
  .mdc-checkbox--disabled.mat-mdc-checkbox-disabled-interactive .mdc-checkbox:hover > .mdc-checkbox__native-control ~ .mdc-checkbox__background,
  .mdc-checkbox--disabled.mat-mdc-checkbox-disabled-interactive .mdc-checkbox .mdc-checkbox__native-control:focus ~ .mdc-checkbox__background,
  .mdc-checkbox--disabled.mat-mdc-checkbox-disabled-interactive .mdc-checkbox__background {
    border-color: GrayText;
  }
}
.mdc-checkbox--disabled.mat-mdc-checkbox-disabled-interactive .mdc-checkbox__native-control:checked ~ .mdc-checkbox__background,
.mdc-checkbox--disabled.mat-mdc-checkbox-disabled-interactive .mdc-checkbox__native-control:indeterminate ~ .mdc-checkbox__background {
  background-color: var(--mat-checkbox-disabled-selected-icon-color, color-mix(in srgb, var(--mat-sys-on-surface) 38%, transparent));
  border-color: transparent;
}

.mdc-checkbox__checkmark {
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  left: 0;
  width: 100%;
  opacity: 0;
  transition: opacity 180ms cubic-bezier(0.4, 0, 0.6, 1);
  color: var(--mat-checkbox-selected-checkmark-color, var(--mat-sys-on-primary));
}
@media (forced-colors: active) {
  .mdc-checkbox__checkmark {
    color: CanvasText;
  }
}

.mdc-checkbox--disabled .mdc-checkbox__checkmark, .mdc-checkbox--disabled.mat-mdc-checkbox-disabled-interactive .mdc-checkbox__checkmark {
  color: var(--mat-checkbox-disabled-selected-checkmark-color, var(--mat-sys-surface));
}
@media (forced-colors: active) {
  .mdc-checkbox--disabled .mdc-checkbox__checkmark, .mdc-checkbox--disabled.mat-mdc-checkbox-disabled-interactive .mdc-checkbox__checkmark {
    color: GrayText;
  }
}

.mdc-checkbox__checkmark-path {
  transition: stroke-dashoffset 180ms cubic-bezier(0.4, 0, 0.6, 1);
  stroke: currentColor;
  stroke-width: 3.12px;
  stroke-dashoffset: 29.7833385;
  stroke-dasharray: 29.7833385;
}

.mdc-checkbox__mixedmark {
  width: 100%;
  height: 0;
  transform: scaleX(0) rotate(0deg);
  border-width: 1px;
  border-style: solid;
  opacity: 0;
  transition: opacity 90ms cubic-bezier(0.4, 0, 0.6, 1), transform 90ms cubic-bezier(0.4, 0, 0.6, 1);
  border-color: var(--mat-checkbox-selected-checkmark-color, var(--mat-sys-on-primary));
}
@media (forced-colors: active) {
  .mdc-checkbox__mixedmark {
    margin: 0 1px;
  }
}

.mdc-checkbox--disabled .mdc-checkbox__mixedmark, .mdc-checkbox--disabled.mat-mdc-checkbox-disabled-interactive .mdc-checkbox__mixedmark {
  border-color: var(--mat-checkbox-disabled-selected-checkmark-color, var(--mat-sys-surface));
}
@media (forced-colors: active) {
  .mdc-checkbox--disabled .mdc-checkbox__mixedmark, .mdc-checkbox--disabled.mat-mdc-checkbox-disabled-interactive .mdc-checkbox__mixedmark {
    border-color: GrayText;
  }
}

.mdc-checkbox--anim-unchecked-checked .mdc-checkbox__background,
.mdc-checkbox--anim-unchecked-indeterminate .mdc-checkbox__background,
.mdc-checkbox--anim-checked-unchecked .mdc-checkbox__background,
.mdc-checkbox--anim-indeterminate-unchecked .mdc-checkbox__background {
  animation-duration: 180ms;
  animation-timing-function: linear;
}

.mdc-checkbox--anim-unchecked-checked .mdc-checkbox__checkmark-path {
  animation: mdc-checkbox-unchecked-checked-checkmark-path 180ms linear;
  transition: none;
}

.mdc-checkbox--anim-unchecked-indeterminate .mdc-checkbox__mixedmark {
  animation: mdc-checkbox-unchecked-indeterminate-mixedmark 90ms linear;
  transition: none;
}

.mdc-checkbox--anim-checked-unchecked .mdc-checkbox__checkmark-path {
  animation: mdc-checkbox-checked-unchecked-checkmark-path 90ms linear;
  transition: none;
}

.mdc-checkbox--anim-checked-indeterminate .mdc-checkbox__checkmark {
  animation: mdc-checkbox-checked-indeterminate-checkmark 90ms linear;
  transition: none;
}
.mdc-checkbox--anim-checked-indeterminate .mdc-checkbox__mixedmark {
  animation: mdc-checkbox-checked-indeterminate-mixedmark 90ms linear;
  transition: none;
}

.mdc-checkbox--anim-indeterminate-checked .mdc-checkbox__checkmark {
  animation: mdc-checkbox-indeterminate-checked-checkmark 500ms linear;
  transition: none;
}
.mdc-checkbox--anim-indeterminate-checked .mdc-checkbox__mixedmark {
  animation: mdc-checkbox-indeterminate-checked-mixedmark 500ms linear;
  transition: none;
}

.mdc-checkbox--anim-indeterminate-unchecked .mdc-checkbox__mixedmark {
  animation: mdc-checkbox-indeterminate-unchecked-mixedmark 300ms linear;
  transition: none;
}

.mdc-checkbox__native-control:checked ~ .mdc-checkbox__background,
.mdc-checkbox__native-control:indeterminate ~ .mdc-checkbox__background {
  transition: border-color 90ms cubic-bezier(0, 0, 0.2, 1), background-color 90ms cubic-bezier(0, 0, 0.2, 1);
}
.mdc-checkbox__native-control:checked ~ .mdc-checkbox__background > .mdc-checkbox__checkmark > .mdc-checkbox__checkmark-path,
.mdc-checkbox__native-control:indeterminate ~ .mdc-checkbox__background > .mdc-checkbox__checkmark > .mdc-checkbox__checkmark-path {
  stroke-dashoffset: 0;
}

.mdc-checkbox__native-control:checked ~ .mdc-checkbox__background > .mdc-checkbox__checkmark {
  transition: opacity 180ms cubic-bezier(0, 0, 0.2, 1), transform 180ms cubic-bezier(0, 0, 0.2, 1);
  opacity: 1;
}
.mdc-checkbox__native-control:checked ~ .mdc-checkbox__background > .mdc-checkbox__mixedmark {
  transform: scaleX(1) rotate(-45deg);
}

.mdc-checkbox__native-control:indeterminate ~ .mdc-checkbox__background > .mdc-checkbox__checkmark {
  transform: rotate(45deg);
  opacity: 0;
  transition: opacity 90ms cubic-bezier(0.4, 0, 0.6, 1), transform 90ms cubic-bezier(0.4, 0, 0.6, 1);
}
.mdc-checkbox__native-control:indeterminate ~ .mdc-checkbox__background > .mdc-checkbox__mixedmark {
  transform: scaleX(1) rotate(0deg);
  opacity: 1;
}

@keyframes mdc-checkbox-unchecked-checked-checkmark-path {
  0%, 50% {
    stroke-dashoffset: 29.7833385;
  }
  50% {
    animation-timing-function: cubic-bezier(0, 0, 0.2, 1);
  }
  100% {
    stroke-dashoffset: 0;
  }
}
@keyframes mdc-checkbox-unchecked-indeterminate-mixedmark {
  0%, 68.2% {
    transform: scaleX(0);
  }
  68.2% {
    animation-timing-function: cubic-bezier(0, 0, 0, 1);
  }
  100% {
    transform: scaleX(1);
  }
}
@keyframes mdc-checkbox-checked-unchecked-checkmark-path {
  from {
    animation-timing-function: cubic-bezier(0.4, 0, 1, 1);
    opacity: 1;
    stroke-dashoffset: 0;
  }
  to {
    opacity: 0;
    stroke-dashoffset: -29.7833385;
  }
}
@keyframes mdc-checkbox-checked-indeterminate-checkmark {
  from {
    animation-timing-function: cubic-bezier(0, 0, 0.2, 1);
    transform: rotate(0deg);
    opacity: 1;
  }
  to {
    transform: rotate(45deg);
    opacity: 0;
  }
}
@keyframes mdc-checkbox-indeterminate-checked-checkmark {
  from {
    animation-timing-function: cubic-bezier(0.14, 0, 0, 1);
    transform: rotate(45deg);
    opacity: 0;
  }
  to {
    transform: rotate(360deg);
    opacity: 1;
  }
}
@keyframes mdc-checkbox-checked-indeterminate-mixedmark {
  from {
    animation-timing-function: cubic-bezier(0, 0, 0.2, 1);
    transform: rotate(-45deg);
    opacity: 0;
  }
  to {
    transform: rotate(0deg);
    opacity: 1;
  }
}
@keyframes mdc-checkbox-indeterminate-checked-mixedmark {
  from {
    animation-timing-function: cubic-bezier(0.14, 0, 0, 1);
    transform: rotate(0deg);
    opacity: 1;
  }
  to {
    transform: rotate(315deg);
    opacity: 0;
  }
}
@keyframes mdc-checkbox-indeterminate-unchecked-mixedmark {
  0% {
    animation-timing-function: linear;
    transform: scaleX(1);
    opacity: 1;
  }
  32.8%, 100% {
    transform: scaleX(0);
    opacity: 0;
  }
}
.mat-mdc-checkbox {
  display: inline-block;
  position: relative;
  -webkit-tap-highlight-color: transparent;
}
.mat-mdc-checkbox._mat-animation-noopable > .mat-internal-form-field > .mdc-checkbox > .mat-mdc-checkbox-touch-target,
.mat-mdc-checkbox._mat-animation-noopable > .mat-internal-form-field > .mdc-checkbox > .mdc-checkbox__native-control,
.mat-mdc-checkbox._mat-animation-noopable > .mat-internal-form-field > .mdc-checkbox > .mdc-checkbox__ripple,
.mat-mdc-checkbox._mat-animation-noopable > .mat-internal-form-field > .mdc-checkbox > .mat-mdc-checkbox-ripple::before,
.mat-mdc-checkbox._mat-animation-noopable > .mat-internal-form-field > .mdc-checkbox > .mdc-checkbox__background,
.mat-mdc-checkbox._mat-animation-noopable > .mat-internal-form-field > .mdc-checkbox > .mdc-checkbox__background > .mdc-checkbox__checkmark,
.mat-mdc-checkbox._mat-animation-noopable > .mat-internal-form-field > .mdc-checkbox > .mdc-checkbox__background > .mdc-checkbox__checkmark > .mdc-checkbox__checkmark-path,
.mat-mdc-checkbox._mat-animation-noopable > .mat-internal-form-field > .mdc-checkbox > .mdc-checkbox__background > .mdc-checkbox__mixedmark {
  transition: none !important;
  animation: none !important;
}
.mat-mdc-checkbox label {
  cursor: pointer;
}
.mat-mdc-checkbox .mat-internal-form-field {
  color: var(--mat-checkbox-label-text-color, var(--mat-sys-on-surface));
  font-family: var(--mat-checkbox-label-text-font, var(--mat-sys-body-medium-font));
  line-height: var(--mat-checkbox-label-text-line-height, var(--mat-sys-body-medium-line-height));
  font-size: var(--mat-checkbox-label-text-size, var(--mat-sys-body-medium-size));
  letter-spacing: var(--mat-checkbox-label-text-tracking, var(--mat-sys-body-medium-tracking));
  font-weight: var(--mat-checkbox-label-text-weight, var(--mat-sys-body-medium-weight));
}
.mat-mdc-checkbox.mat-mdc-checkbox-disabled.mat-mdc-checkbox-disabled-interactive {
  pointer-events: auto;
}
.mat-mdc-checkbox.mat-mdc-checkbox-disabled.mat-mdc-checkbox-disabled-interactive input {
  cursor: default;
}
.mat-mdc-checkbox.mat-mdc-checkbox-disabled label {
  cursor: default;
  color: var(--mat-checkbox-disabled-label-color, color-mix(in srgb, var(--mat-sys-on-surface) 38%, transparent));
}
@media (forced-colors: active) {
  .mat-mdc-checkbox.mat-mdc-checkbox-disabled label {
    color: GrayText;
  }
}
.mat-mdc-checkbox label:empty {
  display: none;
}
.mat-mdc-checkbox .mdc-checkbox__ripple {
  opacity: 0;
}

.mat-mdc-checkbox .mat-mdc-checkbox-ripple,
.mdc-checkbox__ripple {
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  position: absolute;
  border-radius: 50%;
  pointer-events: none;
}
.mat-mdc-checkbox .mat-mdc-checkbox-ripple:not(:empty),
.mdc-checkbox__ripple:not(:empty) {
  transform: translateZ(0);
}

.mat-mdc-checkbox-ripple .mat-ripple-element {
  opacity: 0.1;
}

.mat-mdc-checkbox-touch-target {
  position: absolute;
  top: 50%;
  left: 50%;
  height: var(--mat-checkbox-touch-target-size, 48px);
  width: var(--mat-checkbox-touch-target-size, 48px);
  transform: translate(-50%, -50%);
  display: var(--mat-checkbox-touch-target-display, block);
}

.mat-mdc-checkbox .mat-mdc-checkbox-ripple::before {
  border-radius: 50%;
}

.mdc-checkbox__native-control:focus-visible ~ .mat-focus-indicator::before {
  content: "";
}
`],encapsulation:2,changeDetection:0});}return a;})(),rn=(()=>{class a{static ɵfac=function(t){return new(t||a)();};static ɵmod=me({type:a});static ɵinj=re({imports:[Me,pe]});}return a;})();var De=["*"];function Cn(a,o){a&1&&Q(0);}var Tn=["tabListContainer"],Sn=["tabList"],En=["tabListInner"],In=["nextPaginator"],Mn=["previousPaginator"],Pn=["content"];function wn(a,o){}var Rn=["tabBodyWrapper"],Nn=["tabHeader"];function Dn(a,o){}function An(a,o){if(a&1&&D(0,Dn,0,0,"ng-template",12),a&2){let e=m().$implicit;_("cdkPortalOutlet",e.templateLabel);}}function Bn(a,o){if(a&1&&k(0),a&2){let e=m().$implicit;O(e.textLabel);}}function Ln(a,o){if(a&1){let e=A();r(0,"div",7,2),b("click",function(){let n=x(e),i=n.$implicit,d=n.$index,I=m(),P=te(1);return C(I._handleClick(i,P,d));})("cdkFocusChange",function(n){let i=x(e).$index,d=m();return C(d._tabFocusChanged(n,i));}),S(2,"span",8)(3,"div",9),r(4,"span",10)(5,"span",11),w(6,An,1,1,null,12)(7,Bn,1,1),c()()();}if(a&2){let e=o.$implicit,t=o.$index,n=te(1),i=m();X(e.labelClass),E("mdc-tab--active",i.selectedIndex===t),_("id",i._getTabLabelId(e,t))("disabled",e.disabled)("fitInkBarToContent",i.fitInkBarToContent),M("tabIndex",i._getTabIndex(t))("aria-posinset",t+1)("aria-setsize",i._tabs.length)("aria-controls",i._getTabContentId(t))("aria-selected",i.selectedIndex===t)("aria-label",e.ariaLabel||null)("aria-labelledby",!e.ariaLabel&&e.ariaLabelledby?e.ariaLabelledby:null),l(3),_("matRippleTrigger",n)("matRippleDisabled",e.disabled||i.disableRipple),l(3),R(e.templateLabel?6:7);}}function On(a,o){a&1&&Q(0);}function Gn(a,o){if(a&1){let e=A();r(0,"mat-tab-body",13),b("_onCentered",function(){x(e);let n=m();return C(n._removeTabBodyWrapperHeight());})("_onCentering",function(n){x(e);let i=m();return C(i._setTabBodyWrapperHeight(n));})("_beforeCentering",function(n){x(e);let i=m();return C(i._bodyCentered(n));}),c();}if(a&2){let e=o.$implicit,t=o.$index,n=m();X(e.bodyClass),_("id",n._getTabContentId(t))("content",e.content)("position",e.position)("animationDuration",n.animationDuration)("preserveContent",n.preserveContent),M("tabindex",n.contentTabIndex!=null&&n.selectedIndex===t?n.contentTabIndex:null)("aria-labelledby",n._getTabLabelId(e,t))("aria-hidden",n.selectedIndex!==t);}}var Fn=new B("MatTabContent"),$n=(()=>{class a{template=s(le);constructor(){}static ɵfac=function(t){return new(t||a)();};static ɵdir=$({type:a,selectors:[["","matTabContent",""]],features:[V([{provide:Fn,useExisting:a}])]});}return a;})(),zn=new B("MatTabLabel"),ln=new B("MAT_TAB"),Vn=(()=>{class a extends $t{_closestTab=s(ln,{optional:!0});static ɵfac=(()=>{let e;return function(n){return(e||(e=se(a)))(n||a);};})();static ɵdir=$({type:a,selectors:[["","mat-tab-label",""],["","matTabLabel",""]],features:[V([{provide:zn,useExisting:a}]),Y]});}return a;})(),mn=new B("MAT_TAB_GROUP"),Ae=(()=>{class a{_viewContainerRef=s(nt);_closestTabGroup=s(mn,{optional:!0});disabled=!1;get templateLabel(){return this._templateLabel;}set templateLabel(e){this._setTemplateLabelInput(e);}_templateLabel;_explicitContent=void 0;_implicitContent;textLabel="";ariaLabel;ariaLabelledby;labelClass;bodyClass;id=null;_contentPortal=null;get content(){return this._contentPortal;}_stateChanges=new ae();position=null;origin=null;isActive=!1;constructor(){s(_e).load(ue);}ngOnChanges(e){(e.hasOwnProperty("textLabel")||e.hasOwnProperty("disabled"))&&this._stateChanges.next();}ngOnDestroy(){this._stateChanges.complete();}ngOnInit(){this._contentPortal=new Ft(this._explicitContent||this._implicitContent,this._viewContainerRef);}_setTemplateLabelInput(e){e&&e._closestTab===this&&(this._templateLabel=e);}static ɵfac=function(t){return new(t||a)();};static ɵcmp=N({type:a,selectors:[["mat-tab"]],contentQueries:function(t,n,i){if(t&1&&be(i,Vn,5)(i,$n,7,le),t&2){let d;u(d=p())&&(n.templateLabel=d.first),u(d=p())&&(n._explicitContent=d.first);}},viewQuery:function(t,n){if(t&1&&z(le,7),t&2){let i;u(i=p())&&(n._implicitContent=i.first);}},hostAttrs:["hidden",""],hostVars:1,hostBindings:function(t,n){t&2&&M("id",null);},inputs:{disabled:[2,"disabled","disabled",h],textLabel:[0,"label","textLabel"],ariaLabel:[0,"aria-label","ariaLabel"],ariaLabelledby:[0,"aria-labelledby","ariaLabelledby"],labelClass:"labelClass",bodyClass:"bodyClass",id:"id"},exportAs:["matTab"],features:[V([{provide:ln,useExisting:a}]),ce],ngContentSelectors:De,decls:1,vars:0,template:function(t,n){t&1&&(W(),at(0,Cn,1,0,"ng-template"));},encapsulation:2});}return a;})(),Pe="mdc-tab-indicator--active",cn="mdc-tab-indicator--no-transition",we=class{_items;_currentItem;constructor(o){this._items=o;}hide(){this._items.forEach(o=>o.deactivateInkBar()),this._currentItem=void 0;}alignToElement(o){let e=this._items.find(n=>n.elementRef.nativeElement===o),t=this._currentItem;if(e!==t&&(t?.deactivateInkBar(),e)){let n=t?.elementRef.nativeElement.getBoundingClientRect?.();e.activateInkBar(n),this._currentItem=e;}}},Hn=(()=>{class a{_elementRef=s(L);_inkBarElement=null;_inkBarContentElement=null;_fitToContent=!1;get fitInkBarToContent(){return this._fitToContent;}set fitInkBarToContent(e){this._fitToContent!==e&&(this._fitToContent=e,this._inkBarElement&&this._appendInkBarElement());}activateInkBar(e){let t=this._elementRef.nativeElement;if(!e||!t.getBoundingClientRect||!this._inkBarContentElement){t.classList.add(Pe);return;}let n=t.getBoundingClientRect(),i=e.width/n.width,d=e.left-n.left;t.classList.add(cn),this._inkBarContentElement.style.setProperty("transform",`translateX(${d}px) scaleX(${i})`),t.getBoundingClientRect(),t.classList.remove(cn),t.classList.add(Pe),this._inkBarContentElement.style.setProperty("transform","");}deactivateInkBar(){this._elementRef.nativeElement.classList.remove(Pe);}ngOnInit(){this._createInkBarElement();}ngOnDestroy(){this._inkBarElement?.remove(),this._inkBarElement=this._inkBarContentElement=null;}_createInkBarElement(){let e=this._elementRef.nativeElement.ownerDocument||document,t=this._inkBarElement=e.createElement("span"),n=this._inkBarContentElement=e.createElement("span");t.className="mdc-tab-indicator",n.className="mdc-tab-indicator__content mdc-tab-indicator__content--underline",t.appendChild(this._inkBarContentElement),this._appendInkBarElement();}_appendInkBarElement(){this._inkBarElement;let e=this._fitToContent?this._elementRef.nativeElement.querySelector(".mdc-tab__content"):this._elementRef.nativeElement;e.appendChild(this._inkBarElement);}static ɵfac=function(t){return new(t||a)();};static ɵdir=$({type:a,inputs:{fitInkBarToContent:[2,"fitInkBarToContent","fitInkBarToContent",h]}});}return a;})();var bn=(()=>{class a extends Hn{elementRef=s(L);disabled=!1;focus(){this.elementRef.nativeElement.focus();}getOffsetLeft(){return this.elementRef.nativeElement.offsetLeft;}getOffsetWidth(){return this.elementRef.nativeElement.offsetWidth;}static ɵfac=(()=>{let e;return function(n){return(e||(e=se(a)))(n||a);};})();static ɵdir=$({type:a,selectors:[["","matTabLabelWrapper",""]],hostVars:3,hostBindings:function(t,n){t&2&&(M("aria-disabled",!!n.disabled),E("mat-mdc-tab-disabled",n.disabled));},inputs:{disabled:[2,"disabled","disabled",h]},features:[Y]});}return a;})(),sn={passive:!0},Wn=650,Qn=100,Xn=(()=>{class a{_elementRef=s(L);_changeDetectorRef=s(j);_viewportRuler=s(Gt);_dir=s(Te,{optional:!0});_ngZone=s(F);_platform=s(Ce);_sharedResizeObserver=s(It);_injector=s(ge);_renderer=s(fe);_animationsDisabled=K();_eventCleanups;_scrollDistance=0;_selectedIndexChanged=!1;_destroyed=new ae();_showPaginationControls=!1;_disableScrollAfter=!0;_disableScrollBefore=!0;_tabLabelCount;_scrollDistanceChanged=!1;_keyManager;_currentTextContent;_stopScrolling=new ae();disablePagination=!1;get selectedIndex(){return this._selectedIndex;}set selectedIndex(e){let t=isNaN(e)?0:e;this._selectedIndex!=t&&(this._selectedIndexChanged=!0,this._selectedIndex=t,this._keyManager&&this._keyManager.updateActiveItem(t));}_selectedIndex=0;selectFocusedIndex=new T();indexFocused=new T();constructor(){this._eventCleanups=this._ngZone.runOutsideAngular(()=>[this._renderer.listen(this._elementRef.nativeElement,"mouseleave",()=>this._stopInterval())]);}ngAfterViewInit(){this._eventCleanups.push(this._renderer.listen(this._previousPaginator.nativeElement,"touchstart",()=>this._handlePaginatorPress("before"),sn),this._renderer.listen(this._nextPaginator.nativeElement,"touchstart",()=>this._handlePaginatorPress("after"),sn));}ngAfterContentInit(){let e=this._dir?this._dir.change:Xe("ltr"),t=this._sharedResizeObserver.observe(this._elementRef.nativeElement).pipe(Ke(32),U(this._destroyed)),n=this._viewportRuler.change(150).pipe(U(this._destroyed)),i=()=>{this.updatePagination(),this._alignInkBarToSelectedTab();};this._keyManager=new lt(this._items).withHorizontalOrientation(this._getLayoutDirection()).withHomeAndEnd().withWrap().skipPredicate(()=>!1),this._keyManager.updateActiveItem(Math.max(this._selectedIndex,0)),de(i,{injector:this._injector}),ie(e,n,t,this._items.changes,this._itemsResized()).pipe(U(this._destroyed)).subscribe(()=>{this._ngZone.run(()=>{Promise.resolve().then(()=>{this._scrollDistance=Math.max(0,Math.min(this._getMaxScrollDistance(),this._scrollDistance)),i();});}),this._keyManager?.withHorizontalOrientation(this._getLayoutDirection());}),this._keyManager.change.subscribe(d=>{this.indexFocused.emit(d),this._setTabFocus(d);});}_itemsResized(){return typeof ResizeObserver!="function"?Qe:this._items.changes.pipe(oe(this._items),Ue(e=>new We(t=>this._ngZone.runOutsideAngular(()=>{let n=new ResizeObserver(i=>t.next(i));return e.forEach(i=>n.observe(i.elementRef.nativeElement)),()=>{n.disconnect();};}))),Ze(1),qe(e=>e.some(t=>t.contentRect.width>0&&t.contentRect.height>0)));}ngAfterContentChecked(){this._tabLabelCount!=this._items.length&&(this.updatePagination(),this._tabLabelCount=this._items.length,this._changeDetectorRef.markForCheck()),this._selectedIndexChanged&&(this._scrollToLabel(this._selectedIndex),this._checkScrollingControls(),this._alignInkBarToSelectedTab(),this._selectedIndexChanged=!1,this._changeDetectorRef.markForCheck()),this._scrollDistanceChanged&&(this._updateTabScrollPosition(),this._scrollDistanceChanged=!1,this._changeDetectorRef.markForCheck());}ngOnDestroy(){this._eventCleanups.forEach(e=>e()),this._keyManager?.destroy(),this._destroyed.next(),this._destroyed.complete(),this._stopScrolling.complete();}_handleKeydown(e){if(!dt(e))switch(e.keyCode){case 13:case 32:if(this.focusIndex!==this.selectedIndex){let t=this._items.get(this.focusIndex);t&&!t.disabled&&(this.selectFocusedIndex.emit(this.focusIndex),this._itemSelected(e));}break;default:this._keyManager?.onKeydown(e);}}_onContentChanges(){let e=this._elementRef.nativeElement.textContent;e!==this._currentTextContent&&(this._currentTextContent=e||"",this._ngZone.run(()=>{this.updatePagination(),this._alignInkBarToSelectedTab(),this._changeDetectorRef.markForCheck();}));}updatePagination(){this._checkPaginationEnabled(),this._checkScrollingControls(),this._updateTabScrollPosition();}get focusIndex(){return this._keyManager?this._keyManager.activeItemIndex:0;}set focusIndex(e){!this._isValidIndex(e)||this.focusIndex===e||!this._keyManager||this._keyManager.setActiveItem(e);}_isValidIndex(e){return this._items?!!this._items.toArray()[e]:!0;}_setTabFocus(e){if(this._showPaginationControls&&this._scrollToLabel(e),this._items&&this._items.length){this._items.toArray()[e].focus();let t=this._tabListContainer.nativeElement;this._getLayoutDirection()=="ltr"?t.scrollLeft=0:t.scrollLeft=t.scrollWidth-t.offsetWidth;}}_getLayoutDirection(){return this._dir&&this._dir.value==="rtl"?"rtl":"ltr";}_updateTabScrollPosition(){if(this.disablePagination)return;let e=this.scrollDistance,t=this._getLayoutDirection()==="ltr"?-e:e;this._tabList.nativeElement.style.transform=`translateX(${Math.round(t)}px)`,(this._platform.TRIDENT||this._platform.EDGE)&&(this._tabListContainer.nativeElement.scrollLeft=0);}get scrollDistance(){return this._scrollDistance;}set scrollDistance(e){this._scrollTo(e);}_scrollHeader(e){let t=this._tabListContainer.nativeElement.offsetWidth,n=(e=="before"?-1:1)*t/3;return this._scrollTo(this._scrollDistance+n);}_handlePaginatorClick(e){this._stopInterval(),this._scrollHeader(e);}_scrollToLabel(e){if(this.disablePagination)return;let t=this._items?this._items.toArray()[e]:null;if(!t)return;let n=this._tabListContainer.nativeElement.offsetWidth,{offsetLeft:i,offsetWidth:d}=t.elementRef.nativeElement,I,P;this._getLayoutDirection()=="ltr"?(I=i,P=I+d):(P=this._tabListInner.nativeElement.offsetWidth-i,I=P-d);let H=this.scrollDistance,Z=this.scrollDistance+n;I<H?this.scrollDistance-=H-I:P>Z&&(this.scrollDistance+=Math.min(P-Z,I-H));}_checkPaginationEnabled(){if(this.disablePagination)this._showPaginationControls=!1;else{let e=this._tabListInner.nativeElement.scrollWidth,t=this._elementRef.nativeElement.offsetWidth,n=e-t>=5;n||(this.scrollDistance=0),n!==this._showPaginationControls&&(this._showPaginationControls=n,this._changeDetectorRef.markForCheck());}}_checkScrollingControls(){this.disablePagination?this._disableScrollAfter=this._disableScrollBefore=!0:(this._disableScrollBefore=this.scrollDistance==0,this._disableScrollAfter=this.scrollDistance==this._getMaxScrollDistance(),this._changeDetectorRef.markForCheck());}_getMaxScrollDistance(){let e=this._tabListInner.nativeElement.scrollWidth,t=this._tabListContainer.nativeElement.offsetWidth;return e-t||0;}_alignInkBarToSelectedTab(){let e=this._items&&this._items.length?this._items.toArray()[this.selectedIndex]:null,t=e?e.elementRef.nativeElement:null;t?this._inkBar.alignToElement(t):this._inkBar.hide();}_stopInterval(){this._stopScrolling.next();}_handlePaginatorPress(e,t){t&&t.button!=null&&t.button!==0||(this._stopInterval(),je(Wn,Qn).pipe(U(ie(this._stopScrolling,this._destroyed))).subscribe(()=>{let{maxScrollDistance:n,distance:i}=this._scrollHeader(e);(i===0||i>=n)&&this._stopInterval();}));}_scrollTo(e){if(this.disablePagination)return{maxScrollDistance:0,distance:0};let t=this._getMaxScrollDistance();return this._scrollDistance=Math.max(0,Math.min(t,e)),this._scrollDistanceChanged=!0,this._checkScrollingControls(),{maxScrollDistance:t,distance:this._scrollDistance};}static ɵfac=function(t){return new(t||a)();};static ɵdir=$({type:a,inputs:{disablePagination:[2,"disablePagination","disablePagination",h],selectedIndex:[2,"selectedIndex","selectedIndex",q]},outputs:{selectFocusedIndex:"selectFocusedIndex",indexFocused:"indexFocused"}});}return a;})(),jn=(()=>{class a extends Xn{_items;_tabListContainer;_tabList;_tabListInner;_nextPaginator;_previousPaginator;_inkBar;ariaLabel;ariaLabelledby;disableRipple=!1;ngAfterContentInit(){this._inkBar=new we(this._items),super.ngAfterContentInit();}_itemSelected(e){e.preventDefault();}static ɵfac=(()=>{let e;return function(n){return(e||(e=se(a)))(n||a);};})();static ɵcmp=N({type:a,selectors:[["mat-tab-header"]],contentQueries:function(t,n,i){if(t&1&&be(i,bn,4),t&2){let d;u(d=p())&&(n._items=d);}},viewQuery:function(t,n){if(t&1&&z(Tn,7)(Sn,7)(En,7)(In,5)(Mn,5),t&2){let i;u(i=p())&&(n._tabListContainer=i.first),u(i=p())&&(n._tabList=i.first),u(i=p())&&(n._tabListInner=i.first),u(i=p())&&(n._nextPaginator=i.first),u(i=p())&&(n._previousPaginator=i.first);}},hostAttrs:[1,"mat-mdc-tab-header"],hostVars:4,hostBindings:function(t,n){t&2&&E("mat-mdc-tab-header-pagination-controls-enabled",n._showPaginationControls)("mat-mdc-tab-header-rtl",n._getLayoutDirection()=="rtl");},inputs:{ariaLabel:[0,"aria-label","ariaLabel"],ariaLabelledby:[0,"aria-labelledby","ariaLabelledby"],disableRipple:[2,"disableRipple","disableRipple",h]},features:[Y],ngContentSelectors:De,decls:13,vars:10,consts:[["previousPaginator",""],["tabListContainer",""],["tabList",""],["tabListInner",""],["nextPaginator",""],["mat-ripple","",1,"mat-mdc-tab-header-pagination","mat-mdc-tab-header-pagination-before",3,"click","mousedown","touchend","matRippleDisabled"],[1,"mat-mdc-tab-header-pagination-chevron"],[1,"mat-mdc-tab-label-container",3,"keydown"],["role","tablist",1,"mat-mdc-tab-list",3,"cdkObserveContent"],[1,"mat-mdc-tab-labels"],["mat-ripple","",1,"mat-mdc-tab-header-pagination","mat-mdc-tab-header-pagination-after",3,"mousedown","click","touchend","matRippleDisabled"]],template:function(t,n){t&1&&(W(),r(0,"div",5,0),b("click",function(){return n._handlePaginatorClick("before");})("mousedown",function(d){return n._handlePaginatorPress("before",d);})("touchend",function(){return n._stopInterval();}),S(2,"div",6),c(),r(3,"div",7,1),b("keydown",function(d){return n._handleKeydown(d);}),r(5,"div",8,2),b("cdkObserveContent",function(){return n._onContentChanges();}),r(7,"div",9,3),Q(9),c()()(),r(10,"div",10,4),b("mousedown",function(d){return n._handlePaginatorPress("after",d);})("click",function(){return n._handlePaginatorClick("after");})("touchend",function(){return n._stopInterval();}),S(12,"div",6),c()),t&2&&(E("mat-mdc-tab-header-pagination-disabled",n._disableScrollBefore),_("matRippleDisabled",n._disableScrollBefore||n.disableRipple),l(3),E("_mat-animation-noopable",n._animationsDisabled),l(2),M("aria-label",n.ariaLabel||null)("aria-labelledby",n.ariaLabelledby||null),l(5),E("mat-mdc-tab-header-pagination-disabled",n._disableScrollAfter),_("matRippleDisabled",n._disableScrollAfter||n.disableRipple));},dependencies:[ne,st],styles:[`.mat-mdc-tab-header {
  display: flex;
  overflow: hidden;
  position: relative;
  flex-shrink: 0;
}

.mdc-tab-indicator .mdc-tab-indicator__content {
  transition-duration: var(--mat-tab-animation-duration, 250ms);
}

.mat-mdc-tab-header-pagination {
  -webkit-user-select: none;
  user-select: none;
  position: relative;
  display: none;
  justify-content: center;
  align-items: center;
  min-width: 32px;
  cursor: pointer;
  z-index: 2;
  -webkit-tap-highlight-color: transparent;
  touch-action: none;
  box-sizing: content-box;
  outline: 0;
}
.mat-mdc-tab-header-pagination::-moz-focus-inner {
  border: 0;
}
.mat-mdc-tab-header-pagination .mat-ripple-element {
  opacity: 0.12;
  background-color: var(--mat-tab-inactive-ripple-color, var(--mat-sys-on-surface));
}
.mat-mdc-tab-header-pagination-controls-enabled .mat-mdc-tab-header-pagination {
  display: flex;
}

.mat-mdc-tab-header-pagination-before,
.mat-mdc-tab-header-rtl .mat-mdc-tab-header-pagination-after {
  padding-left: 4px;
}
.mat-mdc-tab-header-pagination-before .mat-mdc-tab-header-pagination-chevron,
.mat-mdc-tab-header-rtl .mat-mdc-tab-header-pagination-after .mat-mdc-tab-header-pagination-chevron {
  transform: rotate(-135deg);
}

.mat-mdc-tab-header-rtl .mat-mdc-tab-header-pagination-before,
.mat-mdc-tab-header-pagination-after {
  padding-right: 4px;
}
.mat-mdc-tab-header-rtl .mat-mdc-tab-header-pagination-before .mat-mdc-tab-header-pagination-chevron,
.mat-mdc-tab-header-pagination-after .mat-mdc-tab-header-pagination-chevron {
  transform: rotate(45deg);
}

.mat-mdc-tab-header-pagination-chevron {
  border-style: solid;
  border-width: 2px 2px 0 0;
  height: 8px;
  width: 8px;
  border-color: var(--mat-tab-pagination-icon-color, var(--mat-sys-on-surface));
}

.mat-mdc-tab-header-pagination-disabled {
  box-shadow: none;
  cursor: default;
  pointer-events: none;
}
.mat-mdc-tab-header-pagination-disabled .mat-mdc-tab-header-pagination-chevron {
  opacity: 0.4;
}

.mat-mdc-tab-list {
  flex-grow: 1;
  position: relative;
  transition: transform 500ms cubic-bezier(0.35, 0, 0.25, 1);
}
._mat-animation-noopable .mat-mdc-tab-list {
  transition: none;
}

.mat-mdc-tab-label-container {
  display: flex;
  flex-grow: 1;
  overflow: hidden;
  z-index: 1;
  border-bottom-style: solid;
  border-bottom-width: var(--mat-tab-divider-height, 1px);
  border-bottom-color: var(--mat-tab-divider-color, var(--mat-sys-surface-variant));
}
.mat-mdc-tab-group-inverted-header .mat-mdc-tab-label-container {
  border-bottom: none;
  border-top-style: solid;
  border-top-width: var(--mat-tab-divider-height, 1px);
  border-top-color: var(--mat-tab-divider-color, var(--mat-sys-surface-variant));
}

.mat-mdc-tab-labels {
  display: flex;
  flex: 1 0 auto;
}
[mat-align-tabs=center] > .mat-mdc-tab-header .mat-mdc-tab-labels {
  justify-content: center;
}
[mat-align-tabs=end] > .mat-mdc-tab-header .mat-mdc-tab-labels {
  justify-content: flex-end;
}
.cdk-drop-list .mat-mdc-tab-labels, .mat-mdc-tab-labels.cdk-drop-list {
  min-height: var(--mat-tab-container-height, 48px);
}

.mat-mdc-tab::before {
  margin: 5px;
}
@media (forced-colors: active) {
  .mat-mdc-tab[aria-disabled=true] {
    color: GrayText;
  }
}
`],encapsulation:2});}return a;})(),qn=new B("MAT_TABS_CONFIG"),dn=(()=>{class a extends Se{_host=s(Re);_ngZone=s(F);_centeringSub=G.EMPTY;_leavingSub=G.EMPTY;constructor(){super();}ngOnInit(){super.ngOnInit(),this._centeringSub=this._host._beforeCentering.pipe(oe(this._host._isCenterPosition())).subscribe(e=>{this._host._content&&e&&!this.hasAttached()&&this._ngZone.run(()=>{Promise.resolve().then(),this.attach(this._host._content);});}),this._leavingSub=this._host._afterLeavingCenter.subscribe(()=>{this._host.preserveContent||this._ngZone.run(()=>this.detach());});}ngOnDestroy(){super.ngOnDestroy(),this._centeringSub.unsubscribe(),this._leavingSub.unsubscribe();}static ɵfac=function(t){return new(t||a)();};static ɵdir=$({type:a,selectors:[["","matTabBodyHost",""]],features:[Y]});}return a;})(),Re=(()=>{class a{_elementRef=s(L);_dir=s(Te,{optional:!0});_ngZone=s(F);_injector=s(ge);_renderer=s(fe);_diAnimationsDisabled=K();_eventCleanups;_initialized=!1;_fallbackTimer;_positionIndex;_dirChangeSubscription=G.EMPTY;_position;_previousPosition;_onCentering=new T();_beforeCentering=new T();_afterLeavingCenter=new T();_onCentered=new T(!0);_portalHost;_contentElement;_content;animationDuration="500ms";preserveContent=!1;set position(e){this._positionIndex=e,this._computePositionAnimationState();}constructor(){if(this._dir){let e=s(j);this._dirChangeSubscription=this._dir.change.subscribe(t=>{this._computePositionAnimationState(t),e.markForCheck();});}}ngOnInit(){this._bindTransitionEvents(),this._position==="center"&&(this._setActiveClass(!0),de(()=>this._onCentering.emit(this._elementRef.nativeElement.clientHeight),{injector:this._injector})),this._initialized=!0;}ngOnDestroy(){clearTimeout(this._fallbackTimer),this._eventCleanups?.forEach(e=>e()),this._dirChangeSubscription.unsubscribe();}_bindTransitionEvents(){this._ngZone.runOutsideAngular(()=>{let e=this._elementRef.nativeElement,t=n=>{n.target===this._contentElement?.nativeElement&&(this._elementRef.nativeElement.classList.remove("mat-tab-body-animating"),n.type==="transitionend"&&this._transitionDone());};this._eventCleanups=[this._renderer.listen(e,"transitionstart",n=>{n.target===this._contentElement?.nativeElement&&(this._elementRef.nativeElement.classList.add("mat-tab-body-animating"),this._transitionStarted());}),this._renderer.listen(e,"transitionend",t),this._renderer.listen(e,"transitioncancel",t)];});}_transitionStarted(){clearTimeout(this._fallbackTimer);let e=this._position==="center";this._beforeCentering.emit(e),e&&this._onCentering.emit(this._elementRef.nativeElement.clientHeight);}_transitionDone(){this._position==="center"?this._onCentered.emit():this._previousPosition==="center"&&this._afterLeavingCenter.emit();}_setActiveClass(e){this._elementRef.nativeElement.classList.toggle("mat-mdc-tab-body-active",e);}_getLayoutDirection(){return this._dir&&this._dir.value==="rtl"?"rtl":"ltr";}_isCenterPosition(){return this._positionIndex===0;}_computePositionAnimationState(e=this._getLayoutDirection()){this._previousPosition=this._position,this._positionIndex<0?this._position=e=="ltr"?"left":"right":this._positionIndex>0?this._position=e=="ltr"?"right":"left":this._position="center",this._animationsDisabled()?this._simulateTransitionEvents():this._initialized&&(this._position==="center"||this._previousPosition==="center")&&(clearTimeout(this._fallbackTimer),this._fallbackTimer=this._ngZone.runOutsideAngular(()=>setTimeout(()=>this._simulateTransitionEvents(),100)));}_simulateTransitionEvents(){this._transitionStarted(),de(()=>this._transitionDone(),{injector:this._injector});}_animationsDisabled(){return this._diAnimationsDisabled||this.animationDuration==="0ms"||this.animationDuration==="0s";}static ɵfac=function(t){return new(t||a)();};static ɵcmp=N({type:a,selectors:[["mat-tab-body"]],viewQuery:function(t,n){if(t&1&&z(dn,5)(Pn,5),t&2){let i;u(i=p())&&(n._portalHost=i.first),u(i=p())&&(n._contentElement=i.first);}},hostAttrs:[1,"mat-mdc-tab-body"],hostVars:1,hostBindings:function(t,n){t&2&&M("inert",n._position==="center"?null:"");},inputs:{_content:[0,"content","_content"],animationDuration:"animationDuration",preserveContent:"preserveContent",position:"position"},outputs:{_onCentering:"_onCentering",_beforeCentering:"_beforeCentering",_onCentered:"_onCentered"},decls:3,vars:6,consts:[["content",""],["cdkScrollable","",1,"mat-mdc-tab-body-content"],["matTabBodyHost",""]],template:function(t,n){t&1&&(r(0,"div",1,0),D(2,wn,0,0,"ng-template",2),c()),t&2&&E("mat-tab-body-content-left",n._position==="left")("mat-tab-body-content-right",n._position==="right")("mat-tab-body-content-can-animate",n._position==="center"||n._previousPosition==="center");},dependencies:[dn,Ot],styles:[`.mat-mdc-tab-body {
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  position: absolute;
  display: block;
  overflow: hidden;
  outline: 0;
  flex-basis: 100%;
}
.mat-mdc-tab-body.mat-mdc-tab-body-active {
  position: relative;
  overflow-x: hidden;
  overflow-y: auto;
  z-index: 1;
  flex-grow: 1;
}
.mat-mdc-tab-group.mat-mdc-tab-group-dynamic-height .mat-mdc-tab-body.mat-mdc-tab-body-active {
  overflow-y: hidden;
}

.mat-mdc-tab-body-content {
  height: 100%;
  overflow: auto;
  transform: none;
  visibility: hidden;
}
.mat-tab-body-animating > .mat-mdc-tab-body-content, .mat-mdc-tab-body-active > .mat-mdc-tab-body-content {
  visibility: visible;
}
.mat-tab-body-animating > .mat-mdc-tab-body-content {
  min-height: 1px;
}
.mat-mdc-tab-group-dynamic-height .mat-mdc-tab-body-content {
  overflow: hidden;
}

.mat-tab-body-content-can-animate {
  transition: transform var(--mat-tab-animation-duration) 1ms cubic-bezier(0.35, 0, 0.25, 1);
}
.mat-mdc-tab-body-wrapper._mat-animation-noopable .mat-tab-body-content-can-animate {
  transition: none;
}

.mat-tab-body-content-left {
  transform: translate3d(-100%, 0, 0);
}

.mat-tab-body-content-right {
  transform: translate3d(100%, 0, 0);
}
`],encapsulation:2});}return a;})(),_n=(()=>{class a{_elementRef=s(L);_changeDetectorRef=s(j);_ngZone=s(F);_tabsSubscription=G.EMPTY;_tabLabelSubscription=G.EMPTY;_tabBodySubscription=G.EMPTY;_diAnimationsDisabled=K();_allTabs;_tabBodies;_tabBodyWrapper;_tabHeader;_tabs=new tt();_indexToSelect=0;_lastFocusedTabIndex=null;_tabBodyWrapperHeight=0;color;get fitInkBarToContent(){return this._fitInkBarToContent;}set fitInkBarToContent(e){this._fitInkBarToContent=e,this._changeDetectorRef.markForCheck();}_fitInkBarToContent=!1;stretchTabs=!0;alignTabs=null;dynamicHeight=!1;get selectedIndex(){return this._selectedIndex;}set selectedIndex(e){this._indexToSelect=isNaN(e)?null:e;}_selectedIndex=null;headerPosition="above";get animationDuration(){return this._animationDuration;}set animationDuration(e){let t=e+"";this._animationDuration=/^\d+$/.test(t)?e+"ms":t;}_animationDuration;get contentTabIndex(){return this._contentTabIndex;}set contentTabIndex(e){this._contentTabIndex=isNaN(e)?null:e;}_contentTabIndex=null;disablePagination=!1;disableRipple=!1;preserveContent=!1;get backgroundColor(){return this._backgroundColor;}set backgroundColor(e){let t=this._elementRef.nativeElement.classList;t.remove("mat-tabs-with-background",`mat-background-${this.backgroundColor}`),e&&t.add("mat-tabs-with-background",`mat-background-${e}`),this._backgroundColor=e;}_backgroundColor;ariaLabel;ariaLabelledby;selectedIndexChange=new T();focusChange=new T();animationDone=new T();selectedTabChange=new T(!0);_groupId;_isServer=!s(Ce).isBrowser;constructor(){let e=s(qn,{optional:!0});this._groupId=s(he).getId("mat-tab-group-"),this.animationDuration=e&&e.animationDuration?e.animationDuration:"500ms",this.disablePagination=e&&e.disablePagination!=null?e.disablePagination:!1,this.dynamicHeight=e&&e.dynamicHeight!=null?e.dynamicHeight:!1,e?.contentTabIndex!=null&&(this.contentTabIndex=e.contentTabIndex),this.preserveContent=!!e?.preserveContent,this.fitInkBarToContent=e&&e.fitInkBarToContent!=null?e.fitInkBarToContent:!1,this.stretchTabs=e&&e.stretchTabs!=null?e.stretchTabs:!0,this.alignTabs=e&&e.alignTabs!=null?e.alignTabs:null;}ngAfterContentChecked(){let e=this._indexToSelect=this._clampTabIndex(this._indexToSelect);if(this._selectedIndex!=e){let t=this._selectedIndex==null;if(!t){this.selectedTabChange.emit(this._createChangeEvent(e));let n=this._tabBodyWrapper.nativeElement;n.style.minHeight=n.clientHeight+"px";}Promise.resolve().then(()=>{this._tabs.forEach((n,i)=>n.isActive=i===e),t||(this.selectedIndexChange.emit(e),this._tabBodyWrapper.nativeElement.style.minHeight="");});}this._tabs.forEach((t,n)=>{t.position=n-e,this._selectedIndex!=null&&t.position==0&&!t.origin&&(t.origin=e-this._selectedIndex);}),this._selectedIndex!==e&&(this._selectedIndex=e,this._lastFocusedTabIndex=null,this._changeDetectorRef.markForCheck());}ngAfterContentInit(){this._subscribeToAllTabChanges(),this._subscribeToTabLabels(),this._tabsSubscription=this._tabs.changes.subscribe(()=>{let e=this._clampTabIndex(this._indexToSelect);if(e===this._selectedIndex){let t=this._tabs.toArray(),n;for(let i=0;i<t.length;i++)if(t[i].isActive){this._indexToSelect=this._selectedIndex=i,this._lastFocusedTabIndex=null,n=t[i];break;}!n&&t[e]&&Promise.resolve().then(()=>{t[e].isActive=!0,this.selectedTabChange.emit(this._createChangeEvent(e));});}this._changeDetectorRef.markForCheck();});}ngAfterViewInit(){this._tabBodySubscription=this._tabBodies.changes.subscribe(()=>this._bodyCentered(!0));}_subscribeToAllTabChanges(){this._allTabs.changes.pipe(oe(this._allTabs)).subscribe(e=>{this._tabs.reset(e.filter(t=>t._closestTabGroup===this||!t._closestTabGroup)),this._tabs.notifyOnChanges();});}ngOnDestroy(){this._tabs.destroy(),this._tabsSubscription.unsubscribe(),this._tabLabelSubscription.unsubscribe(),this._tabBodySubscription.unsubscribe();}realignInkBar(){this._tabHeader&&this._tabHeader._alignInkBarToSelectedTab();}updatePagination(){this._tabHeader&&this._tabHeader.updatePagination();}focusTab(e){let t=this._tabHeader;t&&(t.focusIndex=e);}_focusChanged(e){this._lastFocusedTabIndex=e,this.focusChange.emit(this._createChangeEvent(e));}_createChangeEvent(e){let t=new Ne();return t.index=e,this._tabs&&this._tabs.length&&(t.tab=this._tabs.toArray()[e]),t;}_subscribeToTabLabels(){this._tabLabelSubscription&&this._tabLabelSubscription.unsubscribe(),this._tabLabelSubscription=ie(...this._tabs.map(e=>e._stateChanges)).subscribe(()=>this._changeDetectorRef.markForCheck());}_clampTabIndex(e){return Math.min(this._tabs.length-1,Math.max(e||0,0));}_getTabLabelId(e,t){return e.id||`${this._groupId}-label-${t}`;}_getTabContentId(e){return`${this._groupId}-content-${e}`;}_setTabBodyWrapperHeight(e){if(!this.dynamicHeight||!this._tabBodyWrapperHeight){this._tabBodyWrapperHeight=e;return;}let t=this._tabBodyWrapper.nativeElement;t.style.height=this._tabBodyWrapperHeight+"px",this._tabBodyWrapper.nativeElement.offsetHeight&&(t.style.height=e+"px");}_removeTabBodyWrapperHeight(){let e=this._tabBodyWrapper.nativeElement;this._tabBodyWrapperHeight=e.clientHeight,e.style.height="",this._ngZone.run(()=>this.animationDone.emit());}_handleClick(e,t,n){t.focusIndex=n,e.disabled||(this.selectedIndex=n);}_getTabIndex(e){let t=this._lastFocusedTabIndex??this.selectedIndex;return e===t?0:-1;}_tabFocusChanged(e,t){e&&e!=="mouse"&&e!=="touch"&&(this._tabHeader.focusIndex=t);}_bodyCentered(e){e&&this._tabBodies?.forEach((t,n)=>t._setActiveClass(n===this._selectedIndex));}_animationsDisabled(){return this._diAnimationsDisabled||this.animationDuration==="0"||this.animationDuration==="0ms";}static ɵfac=function(t){return new(t||a)();};static ɵcmp=N({type:a,selectors:[["mat-tab-group"]],contentQueries:function(t,n,i){if(t&1&&be(i,Ae,5),t&2){let d;u(d=p())&&(n._allTabs=d);}},viewQuery:function(t,n){if(t&1&&z(Rn,5)(Nn,5)(Re,5),t&2){let i;u(i=p())&&(n._tabBodyWrapper=i.first),u(i=p())&&(n._tabHeader=i.first),u(i=p())&&(n._tabBodies=i);}},hostAttrs:[1,"mat-mdc-tab-group"],hostVars:11,hostBindings:function(t,n){t&2&&(M("mat-align-tabs",n.alignTabs),X("mat-"+(n.color||"primary")),rt("--mat-tab-animation-duration",n.animationDuration),E("mat-mdc-tab-group-dynamic-height",n.dynamicHeight)("mat-mdc-tab-group-inverted-header",n.headerPosition==="below")("mat-mdc-tab-group-stretch-tabs",n.stretchTabs));},inputs:{color:"color",fitInkBarToContent:[2,"fitInkBarToContent","fitInkBarToContent",h],stretchTabs:[2,"mat-stretch-tabs","stretchTabs",h],alignTabs:[0,"mat-align-tabs","alignTabs"],dynamicHeight:[2,"dynamicHeight","dynamicHeight",h],selectedIndex:[2,"selectedIndex","selectedIndex",q],headerPosition:"headerPosition",animationDuration:"animationDuration",contentTabIndex:[2,"contentTabIndex","contentTabIndex",q],disablePagination:[2,"disablePagination","disablePagination",h],disableRipple:[2,"disableRipple","disableRipple",h],preserveContent:[2,"preserveContent","preserveContent",h],backgroundColor:"backgroundColor",ariaLabel:[0,"aria-label","ariaLabel"],ariaLabelledby:[0,"aria-labelledby","ariaLabelledby"]},outputs:{selectedIndexChange:"selectedIndexChange",focusChange:"focusChange",animationDone:"animationDone",selectedTabChange:"selectedTabChange"},exportAs:["matTabGroup"],features:[V([{provide:mn,useExisting:a}])],ngContentSelectors:De,decls:9,vars:8,consts:[["tabHeader",""],["tabBodyWrapper",""],["tabNode",""],[3,"indexFocused","selectFocusedIndex","selectedIndex","disableRipple","disablePagination","aria-label","aria-labelledby"],["role","tab","matTabLabelWrapper","","cdkMonitorElementFocus","",1,"mdc-tab","mat-mdc-tab","mat-focus-indicator",3,"id","mdc-tab--active","class","disabled","fitInkBarToContent"],[1,"mat-mdc-tab-body-wrapper"],["role","tabpanel",3,"id","class","content","position","animationDuration","preserveContent"],["role","tab","matTabLabelWrapper","","cdkMonitorElementFocus","",1,"mdc-tab","mat-mdc-tab","mat-focus-indicator",3,"click","cdkFocusChange","id","disabled","fitInkBarToContent"],[1,"mdc-tab__ripple"],["mat-ripple","",1,"mat-mdc-tab-ripple",3,"matRippleTrigger","matRippleDisabled"],[1,"mdc-tab__content"],[1,"mdc-tab__text-label"],[3,"cdkPortalOutlet"],["role","tabpanel",3,"_onCentered","_onCentering","_beforeCentering","id","content","position","animationDuration","preserveContent"]],template:function(t,n){t&1&&(W(),r(0,"mat-tab-header",3,0),b("indexFocused",function(d){return n._focusChanged(d);})("selectFocusedIndex",function(d){return n.selectedIndex=d;}),ve(2,Ln,8,17,"div",4,ke),c(),w(4,On,1,0),r(5,"div",5,1),ve(7,Gn,1,10,"mat-tab-body",6,ke),c()),t&2&&(_("selectedIndex",n.selectedIndex||0)("disableRipple",n.disableRipple)("disablePagination",n.disablePagination),it("aria-label",n.ariaLabel)("aria-labelledby",n.ariaLabelledby),l(2),ye(n._tabs),l(2),R(n._isServer?4:-1),l(),E("_mat-animation-noopable",n._animationsDisabled()),l(2),ye(n._tabs));},dependencies:[jn,bn,ct,ne,Se,Re],styles:[`.mdc-tab {
  min-width: 90px;
  padding: 0 24px;
  display: flex;
  flex: 1 0 auto;
  justify-content: center;
  box-sizing: border-box;
  border: none;
  outline: none;
  text-align: center;
  white-space: nowrap;
  cursor: pointer;
  z-index: 1;
  touch-action: manipulation;
}

.mdc-tab__content {
  display: flex;
  align-items: center;
  justify-content: center;
  height: inherit;
  pointer-events: none;
}

.mdc-tab__text-label {
  transition: 150ms color linear;
  display: inline-block;
  line-height: 1;
  z-index: 2;
}

.mdc-tab--active .mdc-tab__text-label {
  transition-delay: 100ms;
}

._mat-animation-noopable .mdc-tab__text-label {
  transition: none;
}

.mdc-tab-indicator {
  display: flex;
  position: absolute;
  top: 0;
  left: 0;
  justify-content: center;
  width: 100%;
  height: 100%;
  pointer-events: none;
  z-index: 1;
}

.mdc-tab-indicator__content {
  transition: var(--mat-tab-animation-duration, 250ms) transform cubic-bezier(0.4, 0, 0.2, 1);
  transform-origin: left;
  opacity: 0;
}

.mdc-tab-indicator__content--underline {
  align-self: flex-end;
  box-sizing: border-box;
  width: 100%;
  border-top-style: solid;
}

.mdc-tab-indicator--active .mdc-tab-indicator__content {
  opacity: 1;
}

._mat-animation-noopable .mdc-tab-indicator__content, .mdc-tab-indicator--no-transition .mdc-tab-indicator__content {
  transition: none;
}

.mat-mdc-tab-ripple.mat-mdc-tab-ripple {
  position: absolute;
  top: 0;
  left: 0;
  bottom: 0;
  right: 0;
  pointer-events: none;
}

.mat-mdc-tab {
  -webkit-tap-highlight-color: transparent;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
  text-decoration: none;
  background: none;
  height: var(--mat-tab-container-height, 48px);
  font-family: var(--mat-tab-label-text-font, var(--mat-sys-title-small-font));
  font-size: var(--mat-tab-label-text-size, var(--mat-sys-title-small-size));
  letter-spacing: var(--mat-tab-label-text-tracking, var(--mat-sys-title-small-tracking));
  line-height: var(--mat-tab-label-text-line-height, var(--mat-sys-title-small-line-height));
  font-weight: var(--mat-tab-label-text-weight, var(--mat-sys-title-small-weight));
}
.mat-mdc-tab.mdc-tab {
  flex-grow: 0;
}
.mat-mdc-tab .mdc-tab-indicator__content--underline {
  border-color: var(--mat-tab-active-indicator-color, var(--mat-sys-primary));
  border-top-width: var(--mat-tab-active-indicator-height, 2px);
  border-radius: var(--mat-tab-active-indicator-shape, 0);
}
.mat-mdc-tab:hover .mdc-tab__text-label {
  color: var(--mat-tab-inactive-hover-label-text-color, var(--mat-sys-on-surface));
}
.mat-mdc-tab:focus .mdc-tab__text-label {
  color: var(--mat-tab-inactive-focus-label-text-color, var(--mat-sys-on-surface));
}
.mat-mdc-tab.mdc-tab--active .mdc-tab__text-label {
  color: var(--mat-tab-active-label-text-color, var(--mat-sys-on-surface));
}
.mat-mdc-tab.mdc-tab--active .mdc-tab__ripple::before,
.mat-mdc-tab.mdc-tab--active .mat-ripple-element {
  background-color: var(--mat-tab-active-ripple-color, var(--mat-sys-on-surface));
}
.mat-mdc-tab.mdc-tab--active:hover .mdc-tab__text-label {
  color: var(--mat-tab-active-hover-label-text-color, var(--mat-sys-on-surface));
}
.mat-mdc-tab.mdc-tab--active:hover .mdc-tab-indicator__content--underline {
  border-color: var(--mat-tab-active-hover-indicator-color, var(--mat-sys-primary));
}
.mat-mdc-tab.mdc-tab--active:focus .mdc-tab__text-label {
  color: var(--mat-tab-active-focus-label-text-color, var(--mat-sys-on-surface));
}
.mat-mdc-tab.mdc-tab--active:focus .mdc-tab-indicator__content--underline {
  border-color: var(--mat-tab-active-focus-indicator-color, var(--mat-sys-primary));
}
.mat-mdc-tab.mat-mdc-tab-disabled {
  opacity: 0.4;
  pointer-events: none;
}
.mat-mdc-tab.mat-mdc-tab-disabled .mdc-tab__content {
  pointer-events: none;
}
.mat-mdc-tab.mat-mdc-tab-disabled .mdc-tab__ripple::before,
.mat-mdc-tab.mat-mdc-tab-disabled .mat-ripple-element {
  background-color: var(--mat-tab-disabled-ripple-color, var(--mat-sys-on-surface-variant));
}
.mat-mdc-tab .mdc-tab__ripple::before {
  content: "";
  display: block;
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  opacity: 0;
  pointer-events: none;
  background-color: var(--mat-tab-inactive-ripple-color, var(--mat-sys-on-surface));
}
.mat-mdc-tab .mdc-tab__text-label {
  color: var(--mat-tab-inactive-label-text-color, var(--mat-sys-on-surface));
  display: inline-flex;
  align-items: center;
}
.mat-mdc-tab .mdc-tab__content {
  position: relative;
  pointer-events: auto;
}
.mat-mdc-tab:hover .mdc-tab__ripple::before {
  opacity: 0.04;
}
.mat-mdc-tab.cdk-program-focused .mdc-tab__ripple::before, .mat-mdc-tab.cdk-keyboard-focused .mdc-tab__ripple::before {
  opacity: 0.12;
}
.mat-mdc-tab .mat-ripple-element {
  opacity: 0.12;
  background-color: var(--mat-tab-inactive-ripple-color, var(--mat-sys-on-surface));
}
.mat-mdc-tab-group.mat-mdc-tab-group-stretch-tabs > .mat-mdc-tab-header .mat-mdc-tab {
  flex-grow: 1;
}

.mat-mdc-tab-group {
  display: flex;
  flex-direction: column;
  max-width: 100%;
}
.mat-mdc-tab-group.mat-tabs-with-background > .mat-mdc-tab-header, .mat-mdc-tab-group.mat-tabs-with-background > .mat-mdc-tab-header-pagination {
  background-color: var(--mat-tab-background-color);
}
.mat-mdc-tab-group.mat-tabs-with-background.mat-primary > .mat-mdc-tab-header .mat-mdc-tab .mdc-tab__text-label {
  color: var(--mat-tab-foreground-color);
}
.mat-mdc-tab-group.mat-tabs-with-background.mat-primary > .mat-mdc-tab-header .mdc-tab-indicator__content--underline {
  border-color: var(--mat-tab-foreground-color);
}
.mat-mdc-tab-group.mat-tabs-with-background:not(.mat-primary) > .mat-mdc-tab-header .mat-mdc-tab:not(.mdc-tab--active) .mdc-tab__text-label {
  color: var(--mat-tab-foreground-color);
}
.mat-mdc-tab-group.mat-tabs-with-background:not(.mat-primary) > .mat-mdc-tab-header .mat-mdc-tab:not(.mdc-tab--active) .mdc-tab-indicator__content--underline {
  border-color: var(--mat-tab-foreground-color);
}
.mat-mdc-tab-group.mat-tabs-with-background > .mat-mdc-tab-header .mat-mdc-tab-header-pagination-chevron,
.mat-mdc-tab-group.mat-tabs-with-background > .mat-mdc-tab-header .mat-focus-indicator::before, .mat-mdc-tab-group.mat-tabs-with-background > .mat-mdc-tab-header-pagination .mat-mdc-tab-header-pagination-chevron,
.mat-mdc-tab-group.mat-tabs-with-background > .mat-mdc-tab-header-pagination .mat-focus-indicator::before {
  border-color: var(--mat-tab-foreground-color);
}
.mat-mdc-tab-group.mat-tabs-with-background > .mat-mdc-tab-header .mat-ripple-element, .mat-mdc-tab-group.mat-tabs-with-background > .mat-mdc-tab-header .mdc-tab__ripple::before, .mat-mdc-tab-group.mat-tabs-with-background > .mat-mdc-tab-header-pagination .mat-ripple-element, .mat-mdc-tab-group.mat-tabs-with-background > .mat-mdc-tab-header-pagination .mdc-tab__ripple::before {
  background-color: var(--mat-tab-foreground-color);
}
.mat-mdc-tab-group.mat-tabs-with-background > .mat-mdc-tab-header .mat-mdc-tab-header-pagination-chevron, .mat-mdc-tab-group.mat-tabs-with-background > .mat-mdc-tab-header-pagination .mat-mdc-tab-header-pagination-chevron {
  color: var(--mat-tab-foreground-color);
}
.mat-mdc-tab-group.mat-mdc-tab-group-inverted-header {
  flex-direction: column-reverse;
}
.mat-mdc-tab-group.mat-mdc-tab-group-inverted-header .mdc-tab-indicator__content--underline {
  align-self: flex-start;
}

.mat-mdc-tab-body-wrapper {
  position: relative;
  overflow: hidden;
  display: flex;
  transition: height 500ms cubic-bezier(0.35, 0, 0.25, 1);
}
.mat-mdc-tab-body-wrapper._mat-animation-noopable {
  transition: none !important;
  animation: none !important;
}
`],encapsulation:2});}return a;})(),Ne=class{index;tab;};var hn=(()=>{class a{static ɵfac=function(t){return new(t||a)();};static ɵmod=me({type:a});static ɵinj=re({imports:[pe]});}return a;})();function Zn(a,o){a&1&&S(0,"mat-progress-bar",21);}function Un(a,o){if(a&1&&(r(0,"mat-card",22)(1,"mat-card-content"),k(2),c()()),a&2){let e=m();l(2),O(e.error());}}function Yn(a,o){a&1&&(r(0,"th",48),f(1,11),c());}function Jn(a,o){if(a&1&&(r(0,"td",49),k(1),c()),a&2){let e=o.$implicit;l(),O(e.key);}}function ea(a,o){a&1&&(r(0,"th",48),f(1,12),c());}function ta(a,o){if(a&1){let e=A();r(0,"mat-form-field",29)(1,"mat-label"),f(2,13),c(),r(3,"input",30),b("ngModelChange",function(n){x(e);let i=m(2);return C(i.editingValue.set(n));}),c()();}if(a&2){let e=m(2);l(3),_("ngModel",e.editingValue());}}function na(a,o){if(a&1&&(r(0,"code"),k(1),c()),a&2){let e=m().$implicit,t=m();l(),O(t.displayValue(e)||"-");}}function aa(a,o){if(a&1&&(r(0,"td",49),w(1,ta,4,1,"mat-form-field",29)(2,na,2,1,"code"),c()),a&2){let e=o.$implicit,t=m();l(),R(t.editingKey()===e.key?1:2);}}function ia(a,o){a&1&&(r(0,"th",48),f(1,14),c());}function oa(a,o){if(a&1){let e=A();r(0,"mat-checkbox",31),b("ngModelChange",function(n){x(e);let i=m(2);return C(i.editingIsSecret.set(n));}),r(1,"span"),f(2,15),c()();}if(a&2){let e=m(2);_("ngModel",e.editingIsSecret());}}function ra(a,o){if(a&1&&(r(0,"mat-icon"),k(1),c()),a&2){let e=m().$implicit;l(),O(e.isSecret?"lock":"lock_open");}}function ca(a,o){if(a&1&&(r(0,"td",49),w(1,oa,3,1,"mat-checkbox",50)(2,ra,2,1,"mat-icon"),c()),a&2){let e=o.$implicit,t=m();l(),R(t.editingKey()===e.key?1:2);}}function sa(a,o){a&1&&(r(0,"th",48),f(1,16),c());}function da(a,o){if(a&1){let e=A();r(0,"button",51),b("click",function(){x(e);let n=m(2);return C(n.saveEdit());}),r(1,"mat-icon"),k(2,"check"),c()(),r(3,"button",52),b("click",function(){x(e);let n=m(2);return C(n.cancelEdit());}),r(4,"mat-icon"),k(5,"close"),c()();}}function la(a,o){if(a&1){let e=A();r(0,"button",56),b("click",function(){x(e);let n=m(2).$implicit,i=m();return C(i.toggleSecret(n.key));}),r(1,"mat-icon"),k(2),c()();}if(a&2){let e=m(2).$implicit,t=m();l(2),O(t.isSecretVisible(e.key)?"visibility_off":"visibility");}}function ma(a,o){if(a&1){let e=A();w(0,la,3,1,"button",53),r(1,"button",54),b("click",function(){x(e);let n=m().$implicit,i=m();return C(i.startEdit(n));}),r(2,"mat-icon"),k(3,"edit"),c()(),r(4,"button",55),b("click",function(){x(e);let n=m().$implicit,i=m();return C(i.deleteEnvVar(n.key));}),r(5,"mat-icon"),k(6,"delete"),c()();}if(a&2){let e=m().$implicit;R(e.isSecret?0:-1);}}function ba(a,o){if(a&1&&(r(0,"td",49),w(1,da,6,0)(2,ma,7,1),c()),a&2){let e=o.$implicit,t=m();l(),R(t.editingKey()===e.key?1:2);}}function _a(a,o){a&1&&S(0,"tr",57);}function ha(a,o){a&1&&S(0,"tr",58);}var un=class a{api=s(At);dialog=s(Yt);snackBar=s(tn);envVars=v([]);visibleSecrets=v(new Set());loading=v(!1);saving=v(!1);error=v("");rawContent=v("");newKey=v("");newValue=v("");newIsSecret=v(!1);editingKey=v("");editingValue=v("");editingIsSecret=v(!1);displayedColumns=["key","value","secret","actions"];ngOnInit(){this.refresh();}async refresh(){this.loading.set(!0),this.error.set("");try{let[o,e]=await Promise.all([this.api.listEnvVars(),this.api.getRawEnvFile()]);this.envVars.set(o),this.rawContent.set(e);}catch(o){this.error.set(o instanceof Error?o.message:String(o));}finally{this.loading.set(!1);}}startEdit(o){this.editingKey.set(o.key),this.editingValue.set(o.value),this.editingIsSecret.set(o.isSecret);}cancelEdit(){this.editingKey.set(""),this.editingValue.set(""),this.editingIsSecret.set(!1);}async saveEdit(){await this.saveEnvVar(this.editingKey(),this.editingValue(),this.editingIsSecret()),this.cancelEdit();}async addEnvVar(){await this.saveEnvVar(this.newKey().trim(),this.newValue(),this.newIsSecret()),this.newKey.set(""),this.newValue.set(""),this.newIsSecret.set(!1);}async deleteEnvVar(o){let e={title:"Delete environment variable",message:"Delete "+o+"?",cancelLabel:"Cancel",confirmLabel:"Delete"};if(await this.dialog.open(nn,{data:e}).afterClosed().toPromise()){this.saving.set(!0);try{let n=await this.api.deleteEnvVar(o);if(!n.success){this.error.set(n.error);return;}this.snackBar.open("Environment variable deleted",void 0,{duration:2500}),await this.refresh();}finally{this.saving.set(!1);}}}async saveRawEnvFile(){this.saving.set(!0),this.error.set("");try{let o=await this.api.updateRawEnvFile(this.rawContent());if(!o.success){this.error.set(o.error);return;}this.snackBar.open(".env file updated",void 0,{duration:2500}),await this.refresh();}finally{this.saving.set(!1);}}displayValue(o){return!o.isSecret||this.visibleSecrets().has(o.key)?o.value||"":Bt(o.value);}toggleSecret(o){this.visibleSecrets.update(e=>{let t=new Set(e);return t.has(o)?t.delete(o):t.add(o),t;});}isSecretVisible(o){return this.visibleSecrets().has(o);}async saveEnvVar(o,e,t){if(!o){this.error.set("Key is required");return;}this.saving.set(!0),this.error.set("");try{let n=await this.api.updateEnvVar({key:o,value:e,isSecret:t});if(!n.success){this.error.set(n.error);return;}this.snackBar.open("Environment variable saved",void 0,{duration:2500}),await this.refresh();}finally{this.saving.set(!1);}}static ɵfac=function(e){return new(e||a)();};static ɵcmp=N({type:a,selectors:[["app-settings"]],decls:75,vars:9,consts:()=>{let o;o="Environment variables";let e;e="Raw .env";let t;t="Configuration";let n;n="Settings";let i;i="Refresh";let d;d="Add environment variable";let I;I="Key";let P;P="Value";let H;H="Secret";let Z;Z="Save";let Be;Be="Raw .env editor";let Le;Le="File content";let Oe;Oe="Save .env";let Ge;Ge="Key";let Fe;Fe="Value";let $e;$e="Value";let ze;ze="Secret";let Ve;Ve="Secret";let He;return He="Actions",[t,n,i,d,I,P,H,Z,Be,Le,Oe,Ge,Fe,$e,ze,Ve,He,[1,"page-shell"],[1,"page-header"],[1,"eyebrow"],["mat-stroked-button","","type","button",3,"click"],["mode","indeterminate"],[1,"state-card","error-card"],["animationDuration","200ms"],["label",o],[1,"tab-panel"],[1,"form-card"],["mat-card-avatar",""],[1,"settings-form"],["appearance","outline"],["matInput","",3,"ngModelChange","ngModel"],[3,"ngModelChange","ngModel"],["mat-flat-button","","type","button",3,"click"],[1,"table-card"],["mat-table","",3,"dataSource"],["matColumnDef","key"],["mat-header-cell","",4,"matHeaderCellDef"],["mat-cell","",4,"matCellDef"],["matColumnDef","value"],["matColumnDef","secret"],["matColumnDef","actions"],["mat-header-row","",4,"matHeaderRowDef"],["mat-row","",4,"matRowDef","matRowDefColumns"],["label",e],[1,"raw-editor-card"],["appearance","outline",1,"raw-editor"],["matInput","","rows","18",3,"ngModelChange","ngModel"],["align","end"],["mat-header-cell",""],["mat-cell",""],[3,"ngModel"],["mat-icon-button","","type","button","aria-label","Save",3,"click"],["mat-icon-button","","type","button","aria-label","Cancel",3,"click"],["mat-icon-button","","type","button","aria-label","Toggle secret visibility"],["mat-icon-button","","type","button","aria-label","Edit",3,"click"],["mat-icon-button","","type","button","aria-label","Delete",3,"click"],["mat-icon-button","","type","button","aria-label","Toggle secret visibility",3,"click"],["mat-header-row",""],["mat-row",""]];},template:function(e,t){e&1&&(r(0,"section",17)(1,"header",18)(2,"div")(3,"p",19),f(4,0),c(),r(5,"h1"),f(6,1),c()(),r(7,"button",20),b("click",function(){return t.refresh();}),r(8,"mat-icon"),k(9,"refresh"),c(),r(10,"span"),f(11,2),c()()(),w(12,Zn,1,0,"mat-progress-bar",21),w(13,Un,3,1,"mat-card",22),r(14,"mat-tab-group",23)(15,"mat-tab",24)(16,"section",25)(17,"mat-card",26)(18,"mat-card-header")(19,"mat-icon",27),k(20,"add_circle"),c(),r(21,"mat-card-title"),f(22,3),c()(),r(23,"mat-card-content",28)(24,"mat-form-field",29)(25,"mat-label"),f(26,4),c(),r(27,"input",30),b("ngModelChange",function(i){return t.newKey.set(i);}),c()(),r(28,"mat-form-field",29)(29,"mat-label"),f(30,5),c(),r(31,"input",30),b("ngModelChange",function(i){return t.newValue.set(i);}),c()(),r(32,"mat-checkbox",31),b("ngModelChange",function(i){return t.newIsSecret.set(i);}),r(33,"span"),f(34,6),c()(),r(35,"button",32),b("click",function(){return t.addEnvVar();}),r(36,"mat-icon"),k(37,"save"),c(),r(38,"span"),f(39,7),c()()()(),r(40,"mat-card",33)(41,"table",34),J(42,35),D(43,Yn,2,0,"th",36)(44,Jn,2,1,"td",37),ee(),J(45,38),D(46,ea,2,0,"th",36)(47,aa,3,1,"td",37),ee(),J(48,39),D(49,ia,2,0,"th",36)(50,ca,3,1,"td",37),ee(),J(51,40),D(52,sa,2,0,"th",36)(53,ba,3,1,"td",37),ee(),D(54,_a,1,0,"tr",41)(55,ha,1,0,"tr",42),c()()()(),r(56,"mat-tab",43)(57,"section",25)(58,"mat-card",44)(59,"mat-card-header")(60,"mat-icon",27),k(61,"code"),c(),r(62,"mat-card-title"),f(63,8),c()(),r(64,"mat-card-content")(65,"mat-form-field",45)(66,"mat-label"),f(67,9),c(),r(68,"textarea",46),b("ngModelChange",function(i){return t.rawContent.set(i);}),c()()(),r(69,"mat-card-actions",47)(70,"button",32),b("click",function(){return t.saveRawEnvFile();}),r(71,"mat-icon"),k(72,"save"),c(),r(73,"span"),f(74,10),c()()()()()()()()),e&2&&(l(12),R(t.loading()||t.saving()?12:-1),l(),R(t.error()?13:-1),l(14),_("ngModel",t.newKey()),l(4),_("ngModel",t.newValue()),l(),_("ngModel",t.newIsSecret()),l(9),_("dataSource",t.envVars()),l(13),_("matHeaderRowDef",t.displayedColumns),l(),_("matRowDefColumns",t.displayedColumns),l(13),_("ngModel",t.rawContent()));},dependencies:[Et,xt,Tt,St,_t,bt,mt,vt,ht,gt,kt,pt,ft,ut,rn,Me,Lt,Pt,Mt,Rt,wt,en,Jt,Dt,Nt,Ut,zt,Ht,jt,Wt,Vt,qt,Qt,Xt,Kt,Zt,hn,Ae,_n],encapsulation:2});};export{un as SettingsComponent};/**i18n:50891650dad0b0d4917b39accc1751b6ed5530f2b9ce6751d1c125c5daff0d87*/